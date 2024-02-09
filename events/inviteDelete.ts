import { Events } from "discord.js";
import { kfProducer } from "../bot";

async function execute(invite: any) {
    let data = {
        type: "invite_delete",
        invite,
        guild: invite.guild,
        inviter: invite.inviter,
        channel: invite.channel
    };

    await kfProducer.send({
        topic: "event-logs",
        messages: [{ value: JSON.stringify(data) }]
    });
}

const data = {
    name: Events.InviteDelete,
    once: false,
    execute
};

export { data };
