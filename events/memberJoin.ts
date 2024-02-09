import { Events } from "discord.js";
import { kfProducer } from "../bot";

async function execute(event: any) {
    let data = {
        type: "member_join",
        event,
        guild: event.guild,
        user: event.user
    };

    await kfProducer.send({
        topic: "event-logs",
        messages: [{ value: JSON.stringify(data) }]
    });
}

const data = {
    name: Events.GuildMemberAdd,
    once: false,
    execute
};

export { data };
