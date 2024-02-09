import { Events } from "discord.js";
import { kfProducer } from "../bot";

async function execute(messages: any) {
    let data = {
        type: "message_bulk_delete",
        messages,
        channel: messages.first().channel,
        guild: messages.first().guild
    };

    await kfProducer.send({
        topic: "event-logs",
        messages: [{ value: JSON.stringify(data) }]
    });
}

const data = {
    name: Events.MessageBulkDelete,
    once: false,
    execute
};

export { data };
