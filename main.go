package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/joho/godotenv"
)

// Schedule is an array of seconds before the deadline.
type Config struct {
	Schedule  []int      `json:"schedule"`
	Pipelines []Pipeline `json:"pipelines"`
}
type Pipeline struct {
	Sources []struct {
		GuildID    string   `json:"guildId"`
		ChannelIDs []string `json:"channelIds"`
	}
	Recipients []Recipient
}
type Recipient struct {
	GuildID    string   `json:"guildId"`
	ChannelIDs []string `json:"channelIds"`
	UserIDs    []string `json:"userIds"`
}
type VotersAndString struct {
	UserIDs      []string
	ReturnString string
}

func main() {
	// Read config.json and .env
	var c Config
	configBytes, err := os.ReadFile("config.json")
	if err != nil {
		log.Fatalf("Could not read config.json: %v\n", err)
	}
	if err = json.Unmarshal(configBytes, &c); err != nil {
		log.Fatalf("Could not parse config.json: %v\n", err)
	}
	if err = godotenv.Load(".env"); err != nil {
		log.Printf("Could not load .env: %v\nUsing existing env.\n", err)
	}
	discordToken := os.Getenv("DISCORD_TOKEN")
	if discordToken == "" {
		log.Fatalf("Could not read DISCORD_TOKEN in env. Exiting.\n")
	}
	if len(c.Schedule) == 0 {
		log.Fatalln("Schedule is empty.")
	}

	// Make schedule length add up to the two week cycle duration
	slices.Sort(c.Schedule)
	scheduleTotal := 0
	for scheduleVal := range c.Schedule {
		scheduleTotal += scheduleVal
	}
	c.Schedule = append(c.Schedule, 1209600-scheduleTotal)
	reversedSchedule := make([]int, len(c.Schedule))
	for i := 0; i < len(c.Schedule); i++ {
		reversedSchedule[len(reversedSchedule)-i-1] = c.Schedule[i]
	}

	go func() {
		for {
			// Calculate time until the next temperature check voting deadline
			now := time.Now().UTC()
			daysUntilTuesday := int(time.Tuesday-now.Weekday()+7) % 7 // Add 7 to make sure it's positive.

			_, week := now.ISOWeek()
			// Add 14 days (2 weeks) if we passed the deadline today.
			if week%2 == 1 && now.Weekday() == time.Tuesday && now.Hour() >= 19 {
				daysUntilTuesday += 14
			}
			// Add 7 days if we're in an "off week".
			if week%2 == 1 && now.Weekday() > time.Tuesday || week%2 == 0 && now.Weekday() <= time.Tuesday {
				daysUntilTuesday += 7
			}

			votingDeadline := now.AddDate(0, 0, daysUntilTuesday).Truncate(24 * time.Hour)

			for _, s := range reversedSchedule {
				duration := time.Until(votingDeadline.Add(time.Duration(-s) * time.Second))
				// Skip scheduled times we've already missed.
				if duration < 0 {
					continue
				}

				finished := make(chan bool)

				log.Printf("Next message in %f hours\n", duration.Hours())
				time.AfterFunc(duration, func() {
					// TODO: Factor this out
					go func() {
						defer func() {
							finished <- true
							close(finished)
						}()
						// Create Discord session.
						s, err := discordgo.New("Bot " + discordToken)
						if err != nil {
							log.Fatalf("Could not initialize Discord session: %v\n", err)
						}
						log.Println("Successfully created Discord session.")

						s.LogLevel = discordgo.LogWarning
						s.ShouldRetryOnRateLimit = true
						done := make(chan bool)
						s.AddHandler(func(s *discordgo.Session, m *discordgo.Connect) {
							handleConnected(s, m)
							done <- true
						})

						if err = s.Open(); err != nil {
							log.Fatalf("Could not initialize connection to Discord: %v\n", err)
						}
						<-done // Wait to log connection.

						for _, pipeline := range c.Pipelines {
							go func(pipeline Pipeline) {
								msgChan := make(chan *discordgo.Message, 200) // Consider dynamic buffer or more intelligent setup if needed to scale
								getCurrentThreads(s, pipeline, msgChan)       // Get the threads you need to notify people of

								// Process message reacts
								var wg sync.WaitGroup
								votersAndStringChan := make(chan VotersAndString, 200)
								for msg := range msgChan {
									wg.Add(1)
									go func(msg *discordgo.Message) {
										defer wg.Done()
										userIdsWhoVoted, returnString, err := processReactions(s, msg)
										if err != nil {
											log.Printf("Failed while processing reactions: %v\n", err)
											return
										}
										votersAndStringChan <- VotersAndString{userIdsWhoVoted, returnString}
									}(msg)
								}
								wg.Wait()
								close(votersAndStringChan)

								votersAndStrings := make([]VotersAndString, 0)
								for v := range votersAndStringChan {
									votersAndStrings = append(votersAndStrings, v)
								}

								for _, recipient := range pipeline.Recipients {
									startingString := fmt.Sprintf("**Voting ends at <t:%d> (<t:%d:R>).**\n", votingDeadline.Unix(), votingDeadline.Unix())
									go buildStringAndSend(s, startingString, recipient, votersAndStrings)
								}
							}(pipeline)
						}

						s.Close()
					}()
				})
				<-finished
			}
		}
	}()

	// Wait for signal to end program
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, syscall.SIGSEGV, syscall.SIGHUP)
	<-sc
}

func handleConnected(s *discordgo.Session, m *discordgo.Connect) {
	user, err := s.User("@me")
	if err != nil {
		log.Printf("Could not fetch self user: %v\n", err)
		return
	}
	log.Printf("Successfully connected as %v\n", user.Username)
}

func getCurrentThreads(s *discordgo.Session, p Pipeline, msgChan chan *discordgo.Message) {
	defer close(msgChan)

	// Temp check ends every other Tuesday at midnight UTC.
	now := time.Now().UTC()
	daysSinceTuesday := int(now.Weekday()-time.Tuesday+7) % 7

	lastTempCheckEnded := now.AddDate(0, 0, -daysSinceTuesday-7).Truncate(24 * time.Hour)

	// Iterate through sources and find proposal threads which have been created since the end of the last temp check.
	for _, source := range p.Sources {
		for _, channelId := range source.ChannelIDs {
			msgs, err := s.ChannelMessages(channelId, 100, "", "", "")
			if err != nil {
				log.Printf("Error while fetching batch of messages from guild %s, channel %s: %v\n", source.GuildID, channelId, err)
				continue
			}

			for _, msg := range msgs {
				// Ignore non-thread msgs, and messages from before the cutoff
				// See https://docs.juicebox.money/dao/process/ to understand the governance schedule
				if msg.Thread != nil && msg.Timestamp.After(lastTempCheckEnded) {
					msgChan <- msg
				}

			}
		}
	}
}

func processReactions(s *discordgo.Session, m *discordgo.Message) ([]string, string, error) {
	upVotesChan := make(chan []*discordgo.User)
	downVotesChan := make(chan []*discordgo.User)
	errChan := make(chan error)

	go func() {
		// Get a slice of users which have already voted.
		upVotes, err := s.MessageReactions(m.ChannelID, m.ID, "👍", 100, "", "")
		if err != nil {
			errChan <- err
			return
		}
		upVotesChan <- upVotes
	}()
	go func() {
		downVotes, err := s.MessageReactions(m.ChannelID, m.ID, "👎", 100, "", "")
		if err != nil {
			errChan <- err
			return
		}
		downVotesChan <- downVotes
	}()
	var upVotes, downVotes []*discordgo.User
	var err error
	for i := 0; i < 2; i++ {
		select {
		case upVotes = <-upVotesChan:
		case downVotes = <-downVotesChan:
		case err = <-errChan:
			log.Printf("Could not fetch votes for message %s in channel %s: %v\n", m.ID, m.ChannelID, err)
		case <-time.After(60 * time.Second):
			return []string{""}, "", fmt.Errorf("timed out after 60 seconds for %s (channel %s, message %s)", m.Thread.Name, m.ChannelID, m.ID)
		}
	}

	usersWhoVoted := append(upVotes, downVotes...)
	userIdsWhoVoted := make([]string, len(usersWhoVoted))
	for i, user := range usersWhoVoted {
		userIdsWhoVoted[i] = user.ID
	}

	threadLink := fmt.Sprintf("https://discord.com/channels/%s/%s", "775859454780244028", m.Thread.ID) // Hardcode JB GuildID. discordgo.Message.GuildID seems to be broken.
	returnString := fmt.Sprintf("%dx👍, %dx👎 - [%s](%s) ", len(upVotes)-1, len(downVotes)-1, m.Thread.Name, threadLink)
	if len(upVotes)-1 < 10 {
		returnString += " (*below quorum*)"
	}

	return userIdsWhoVoted, returnString, nil
}

func buildStringAndSend(s *discordgo.Session, startingString string, r Recipient, v []VotersAndString) {
	// Check where user IDs overlap.
	voted := make(map[string]bool)
	for _, userId := range r.UserIDs {
		voted[userId] = false
	}

	needToMsg := false
	for _, voterAndString := range v {
		for userId := range voted {
			voted[userId] = false
		}

		returnString := voterAndString.ReturnString
		tagsNeeded := false
		plural := false

		// Track those who voted
		for _, voter := range voterAndString.UserIDs {
			if alreadyFound, ok := voted[voter]; ok && !alreadyFound {
				voted[voter] = true
			}
		}

		// Add tags for those who didn't vote, and update flags depending on how many people voted
		for userId, didVote := range voted {
			if !didVote {
				returnString += fmt.Sprintf(" <@%s>", userId)
				if tagsNeeded {
					plural = true
				}
				tagsNeeded = true
			}
		}

		if tagsNeeded {
			if plural {
				returnString += " haven't voted."
			} else {
				returnString += " hasn't voted."
			}
			needToMsg = true
		}

		startingString += returnString + "\n"
	}

	// If everyone has already voted on everything, there's no need to message.
	if !needToMsg {
		return
	}

	// Split string into 2000 character chunks to stay below Discord's message limit.
	// Do this by line to avoid splitting tags and titles.
	charLimit := 2000 // Discord character limit is 2000 characters.
	var chunks []string
	lines := strings.Split(startingString, "\n")
	chunk := ""
	for _, line := range lines {
		if len(chunk)+len(line) > charLimit {
			chunks = append(chunks, chunk)
			chunk = ""
		}
		chunk += line + "\n"
	}
	if chunk != "" {
		chunks = append(chunks, chunk)
	}

	// Send out messages to recipients by chunk.
	for _, channelId := range r.ChannelIDs {
		go func(channelId string) {
			for _, chunk := range chunks {
				sentMsg, err := s.ChannelMessageSend(channelId, chunk)
				if err != nil {
					log.Printf("Could not send message to channel %s: %v\n", channelId, err)
				}

				_, err = s.ChannelMessageEditComplex(&discordgo.MessageEdit{
					ID:      sentMsg.ID,
					Channel: channelId,
					Content: &sentMsg.Content,
					Flags:   discordgo.MessageFlags(discordgo.MessageFlagsSuppressEmbeds),
				})
				if err != nil {
					log.Printf("Could not edit message to surpress embeds in channel %s: %v\n", channelId, err)
				}
			}
		}(channelId)
	}
}
