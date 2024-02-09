import { Events } from "discord.js";
import { kfProducer } from "../bot";

async function execute(role: any) {
    let data = {
        type: "role_create",
        guild: role.guild,
        role
    };

    await kfProducer.send({
        topic: "event-logs",
        messages: [{ value: JSON.stringify(data) }]
    });
}

const data = {
    name: Events.GuildRoleCreate,
    once: false,
    execute
};

export { data };
