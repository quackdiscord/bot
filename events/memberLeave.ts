import { Events } from "discord.js";
import { kfProducer } from "../bot";

async function execute(event: any) {
    let data = {
        type: "member_leave",
        event,
        guild: event.guild,
        user: event.user,
        roles: event.roles.cache.map((role: any) => {
            return role.id;
        })
    };

    await kfProducer.send({
        topic: "event-logs",
        messages: [{ value: JSON.stringify(data) }]
    });
}

const data = {
    name: Events.GuildMemberRemove,
    once: false,
    execute
};

export { data };
