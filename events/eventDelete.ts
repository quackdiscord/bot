import { Events } from "discord.js";
import { kfProducer } from "../bot";

async function execute(event: any) {
    let data = {
        type: "event_delete",
        event,
        guild: event.guild
    };

    await kfProducer.send({
        topic: "event-logs",
        messages: [{ value: JSON.stringify(data) }]
    });
}

const data = {
    name: Events.GuildScheduledEventDelete,
    once: false,
    execute
};

export { data };
