import { Events } from "discord.js";
import { kfProducer } from "../bot";

async function execute(channel: any) {
    let data = {
        type: "channel_pins_update",
        channel,
        guild: channel.guild
    };

    await kfProducer.send({
        topic: "event-logs",
        messages: [{ value: JSON.stringify(data) }]
    });
}

const data = {
    name: Events.ChannelPinsUpdate,
    once: false,
    execute
};

export { data };
