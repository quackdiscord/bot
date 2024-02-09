import { Events } from "discord.js";

async function execute(oldSticker: any, newSticker: any) {
    // coming later
}

const data = {
    name: Events.GuildStickerUpdate,
    once: false,
    execute
};

export { data };
