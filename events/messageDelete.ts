import { Events } from "discord.js";
import { kfProducer } from "../bot";

async function execute(event: any) {
    let data = {
        type: "message_delete",
        event,
        author: event.author,
        guild: event.guild,
        channel: event.channel,
        attachments: event.attachments
    };

    await kfProducer.send({
        topic: "event-logs",
        messages: [{ value: JSON.stringify(data) }]
    });
}

const data = {
    name: Events.MessageDelete,
    once: false,
    execute
};

export { data };
