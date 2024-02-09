import { Events } from "discord.js";
import { kfProducer } from "../bot";

async function execute(channel: any) {
    let data = {
        type: "channel_create",
        channel,
        guild: channel.guild,
        parent: channel.parent
    };

    await kfProducer.send({
        topic: "event-logs",
        messages: [{ value: JSON.stringify(data) }]
    });
}

const data = {
    name: Events.ChannelCreate,
    once: false,
    execute
};

export { data };
