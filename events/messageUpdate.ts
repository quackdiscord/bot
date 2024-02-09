import { Events } from "discord.js";
import { kfProducer } from "../bot";

async function execute(oldMessage: any, newMessage: any) {
    if (oldMessage.author.bot) {
        return;
    }

    let data = {
        type: "message_update",
        oldMessage,
        newMessage,
        author: oldMessage.author,
        channel: oldMessage.channel
    };

    await kfProducer.send({
        topic: "event-logs",
        messages: [{ value: JSON.stringify(data) }]
    });
}

const data = {
    name: Events.MessageUpdate,
    once: false,
    execute
};

export { data };
