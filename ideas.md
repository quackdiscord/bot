## Moderation Enhancements

1. **Softban** - Ban + immediate unban to purge a user's messages without permanent ban
2. **Tempban** - Temporary ban with automatic unban after a specified duration (like timeout but for bans)
3. **Massban** - Ban multiple users at once via user IDs (useful during raids)
4. **Slowmode** - Quick commands to set/remove slowmode in channels
5. **Dehoist** - Strip special characters from usernames that push them to the top of member lists
6. **Force nickname** - Set a user's nickname and optionally lock it
7. **Warn thresholds** - Auto-escalate punishments when a user hits X warnings (e.g., 3 warns = timeout, 5 = ban)

## Automation

8. **Automod** features (woof does this rn but could be nice to have it in quack for cases)
    - Auto-mod filters - Configurable filters for:
    - Spam detection (repeated messages)
    - Mass mentions (@ everyone abuse, ping spam)
    - Link filtering (whitelist/blacklist domains)
    - Caps lock abuse
    - Slur/word blacklist with regex support
    - Invite link blocking
    - Attachment type filtering (block exe, scr, etc.)
9. **Raid protection** - Detect mass joins (X users in Y seconds) and auto-enable lockdown, require verification, or increase account age requirements
10. **Account age gate** - Kick/timeout accounts younger than X days on join
11. **Anti-nuke** - Detect mass deletions of channels/roles and auto-revoke permissions from the offending user/bot
12. **Duplicate message detection** - Auto-delete copy-paste spam across channels

## User tracking & Intelligence

13. **Watchlist** - Add users to a watchlist that notifies mods when they join voice, send messages in certain channels, etc.
14. **Alt detection** - Flag potential alt accounts based on creation date proximity, similar usernames, IP (if using linked services), or joining right after a ban
15. **Cross-server ban sharing** - Opt-in shared ban database where partnered servers can share bad actors
16. **Join/leave analytics** - Track patterns like users who leave within 24 hours, rejoin frequency, etc.

## Logging Enhancements

15. **Ghost ping logging** - Log when someone deletes a message that contained mentions
16. **Snipe command** - Retrieve the last deleted/edited message in a channel (with permissions)
17. **Voice logs** - Log voice channel joins, leaves, moves, mutes, deafens
18. **Nickname history** - Track username/nickname changes per user
19. **Role change logs** - Log when roles are added/removed from users (eh)

## Communication tools

20. **Scheduled messages** - Post announcements at a specific time or on a recurring schedule
21. **Broadcast** - Send a message to multiple channels at once
22. **Report system** - Build off of the tickets, lets users create a ticket with context automatically. (someones saying stuff in chat, user can do /report <message> [user])

## Advanced

23. **Infraction decay** - Warnings automatically expire after X days of good behavior
24. **Mod activity stats** - Track which mods are most active, response times, actions taken (maybe mods can see their own, admins can see it all)
25. **Context-aware auto-mod** - Different auto-mod rules for different channels (stricter in general, relaxed in memes)
26. **User trust score** - Internal score based on account age, time in server, past infractions - affects auto-mod strictness
27. **Impersonation detection** - Alert when a user changes their name/avatar to match staff
28. **Message backup on ban **- When banning, optionally save their recent messages to a log channel for evidence