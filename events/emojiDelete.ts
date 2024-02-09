import { Events } from "discord.js";
import { kfProducer } from "../bot";

async function execute(emoji: any) {
    let data = {
        type: "emoji_delete",
        emoji,
        guild: emoji.guild
    };

    await kfProducer.send({
        topic: "event-logs",
        messages: [{ value: JSON.stringify(data) }]
    });
}

const data = {
    name: Events.GuildEmojiDelete,
    once: false,
    execute
};

export { data };
