import { Events } from "discord.js";
import { kfProducer } from "../bot";

async function execute(oldChannel: any, newChannel: any) {
    let data = {
        type: "channel_update",
        oldChannel,
        newChannel,
        guild: oldChannel.guild,
        oldParent: oldChannel.parent,
        newParent: newChannel.parent
    };

    await kfProducer.send({
        topic: "event-logs",
        messages: [{ value: JSON.stringify(data) }]
    });
}

const data = {
    name: Events.ChannelUpdate,
    once: false,
    execute
};

export { data };
