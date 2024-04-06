import { ChatInputCommandInteraction, SlashCommandBuilder } from "discord.js";
import embedBuilder from "../../lib/embedBuilder";
import { client, redis } from "../../bot";
import { heapStats } from "bun:jsc";

// create the command
const command = new SlashCommandBuilder().setName("stats").setDescription("Get some stats about the bot.");

// write the function
async function execute(interaction: ChatInputCommandInteraction) {
    const heapstat = heapStats();

    const allCmdData = await redis.hget("seeds:cmds", "total");
    const allCmds = allCmdData ? parseInt(allCmdData as string) : 0;

    const embedData = {
        title: "Quack's Stats",
        description: `Some statistics about Quack\`\`\`asciidoc
Servers    ::   ${interaction.client.guilds.cache.size.toLocaleString("en-US")}
Users      ::   ${interaction.client.users.cache.size.toLocaleString("en-US")} (in cache)
CPU        ::   ${(process.cpuUsage().system / 1000000).toFixed(2)}%
RAM        ::   ${(heapstat.heapSize / 1024 / 1024).toFixed(2)} MB (${(
            (heapstat.heapSize / heapstat.heapCapacity) *
            100
        ).toFixed(2)}%)
Ping       ::   ${Math.round(interaction.client.ws.ping)} ms
Uptime     ::   ${Math.round(process.uptime() / 1000 / 60 / 60 / 24)} days
Library    ::   Discord.js
Runtime    ::   Bun
Cmds. Run  ::   ${allCmds.toLocaleString("en-us")}\`\`\``,
        color: client.mainColor,
        fields: [
            {
                name: "Links",
                value: "[🌐 Website](https://quackbot.xyz) | [<:invite:823987169978613851> Invite](https://quackbot.xyz/invite) | [<:discord:823989269626355793> Support](https://quackbot.xyz/discord)",
                inline: false
            }
        ]
    };

    const embed = embedBuilder(embedData as any);

    await interaction.reply({ embeds: [embed] });
}

// export the command
const data = {
    data: command,
    execute: execute
};

export { data };
