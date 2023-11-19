# Temperature Check Reminder

This program sends out scheduled reminders to vote in JuiceboxDAO [temperature checks](https://docs.juicebox.money/dao/process/). If you'd like help customizing or adapting this, join the [JuiceboxDAO Discord server](https://discord.gg/juicebox) and tag me. My handle is filipvv.

This project isn't built with high-scale use in mind. If you're interested in adapting it for high-volume applications, please message me.

## Setup

1. Clone the repository

2. Install the dependencies by running `go get`

3. Create a `.env` file in the root directory and add your Discord token like so:

```env
DISCORD_TOKEN=your_discord_token
```

If you don't have a Discord bot token, visit the [Discord developers portal](https://discord.com/developers/applications)

4. Configure the `config.json` file to set the schedule and pipelines. Here is an example configuration:

```json
{
  "schedule": [1800, 14400, 86400, 172800],
  "pipelines": [
    {
      "sources": [
        {
          "guildId": "775859454780244028",
          "channelIds": ["873248745771372584"]
        }
      ],
      "recipients": [
        {
          "guildId": "939317843059679252",
          "channelIds": ["1175563104386568272"],
          "userIds": [
            "805601139910508599",
            "966614428596457482",
            "933261640915365929",
            "742143412073136180",
            "818674919898087464",
            "666841845099134988"
          ]
        }
      ]
    }
  ]
}
```

| Key | Type | Description |
---|---|---|
 `schedule` | Array of integers | The schedule in an array of seconds before the deadline. Including `3600` will send out a notification 3,600 seconds before temperature checks end. |
 `pipelines` | Array of objects | The pipelines for the reminders. Each pipeline sends notifications from all of its inputs to all of its outputs. |
 `pipelines.sources` | Array of objects | Sources to watch for proposals and voting. |
 `pipelines.sources.guildId` | String | The Discord guild ID of the source |
 `pipelines.sources.channelIds` | Array of strings | The Discord channel IDs of the source |
 `pipelines.recipients` | Array of objects | The recipients which will receive reminders. |
 `pipelines.recipients.guildId` | String | The Discord guild ID of the recipient. |
 `pipelines.recipients.channelIds` | Array of strings | The Discord channel IDs of the recipient. |
 `pipelines.recipients.userIds` | Array of strings | The Discord user IDs of the recipients to tag in reminders. |

5. Run the program by executing `go run .` or build with `go build .`
