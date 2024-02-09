import { Events } from "discord.js";
import { kfProducer } from "../bot";

async function execute(oldMember: any, newMember: any) {
    let data = {
        type: "member_update",
        guild: newMember.guild,
        oldMember,
        newMember,
        newRoles: newMember.roles.cache.map((role: any) => {
            return role.id;
        }),
        oldRoles: oldMember.roles.cache.map((role: any) => {
            return role.id;
        })
    };

    await kfProducer.send({
        topic: "event-logs",
        messages: [{ value: JSON.stringify(data) }]
    });
}

const data = {
    name: Events.GuildMemberUpdate,
    once: false,
    execute
};

export { data };
