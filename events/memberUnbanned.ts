import { AuditLogEvent, Events } from "discord.js";
import { logger } from "../lib/logger";
import { kfProducer } from "../bot";

async function execute(event: any) {
    // try to get the audit log entry for this event
    let auditLogEntry: any = undefined;
    try {
        auditLogEntry = await event.guild.fetchAuditLogs({
            type: AuditLogEvent.MemberBanAdd,
            limit: 1
        });
    } catch (error) {
        logger.error("Error fetching audit log entry for member_unbanned event", error);
    }

    let banReason = "No reason provided";
    let modID = event.client.user.id;
    if (auditLogEntry) {
        const { executor, reason } = auditLogEntry.entries.first();
        banReason = reason;
        modID = executor.id;
    }

    let data = {
        type: "member_unbanned",
        event,
        guild: event.guild,
        user: event.user,
        reason: banReason,
        moderator: modID
    };

    await kfProducer.send({
        topic: "event-logs",
        messages: [{ value: JSON.stringify(data) }]
    });
}

const data = {
    name: Events.GuildBanRemove,
    once: false,
    execute
};

export { data };
