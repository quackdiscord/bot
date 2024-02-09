import { Events } from "discord.js";
import { kfProducer } from "../bot";

async function execute(oldServer: any, newServer: any) {
    let data = {
        type: "server_update",
        oldServer,
        newServer
    };

    await kfProducer.send({
        topic: "event-logs",
        messages: [{ value: JSON.stringify(data) }]
    });
}

const data = {
    name: Events.GuildUpdate,
    once: false,
    execute
};

export { data };
