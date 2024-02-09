import { Events } from "discord.js";
import { kfProducer } from "../bot";

async function execute(oldRole: any, newRole: any) {
    let data = {
        type: "role_update",
        guild: oldRole.guild,
        oldRole,
        newRole,
        oldPerms: oldRole.permissions,
        newPerms: newRole.permissions
    };

    await kfProducer.send({
        topic: "event-logs",
        messages: [{ value: JSON.stringify(data) }]
    });
}

const data = {
    name: Events.GuildRoleUpdate,
    once: false,
    execute
};

export { data };
