import { Events } from "discord.js";
import { kfProducer } from "../bot";

async function execute(event: any) {
    let data = {
        type: "event_create",
        event,
        guild: event.guild
    };

    await kfProducer.send({
        topic: "event-logs",
        messages: [{ value: JSON.stringify(data) }]
    });
}

const data = {
    name: Events.GuildScheduledEventCreate,
    once: false,
    execute
};

export { data };
