import { Events } from "discord.js";
import { kfProducer } from "../bot";

async function execute(oldEmoji: any, newEmoji: any) {
    let data = {
        type: "emoji_update",
        oldEmoji,
        newEmoji,
        guild: oldEmoji.guild
    };

    await kfProducer.send({
        topic: "event-logs",
        messages: [{ value: JSON.stringify(data) }]
    });
}

const data = {
    name: Events.GuildEmojiUpdate,
    once: false,
    execute
};

export { data };
