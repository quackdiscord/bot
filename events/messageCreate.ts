import { Events } from "discord.js";
import { client } from "../bot";
import config from "../config";

async function execute(message: any) {
    const clean = async (text: string) => {
        // If our input is a promise, await it before continuing
        if (text && text.constructor.name == "Promise") text = await text;

        // If the response isn't a string, `util.inspect()`
        // is used to 'stringify' the code in a safe way that
        // won't error out on objects with circular references
        // (like Collections, for example)
        if (typeof text !== "string") text = require("util").inspect(text, { depth: 1 });

        // Replace symbols with character code alternatives
        text = text.replace(/`/g, "`" + String.fromCharCode(8203)).replace(/@/g, "@" + String.fromCharCode(8203));

        // Send off the cleaned up result
        return text;
    };

    if (message.author.bot) return;
    if (message.author.id !== config.owner_id) return;
    if (message.content.startsWith("!!!")) {
        // initalize the redis, db, and kafka producer variables so they can be used in the eval command
        const redis = require("../bot").redis;
        const db = require("../bot").db;
        const eq = require("drizzle-orm").eq;
        const kfProducer = require("../bot").kfProducer;

        // db schemas
        const cases = require("../schema/case");
        const guilds = require("../schema/guild");
        const users = require("../schema/user");
        const notes = require("../schema/note");
        const logsettings = require("../schema/logsettings");

        const response = await message.reply("Thinking...");
        const [command, ...args] = message.content.slice(4).split(" ");
        if (command === "restart") {
            response.edit("Restarting... check the console for more info.");
            setTimeout(async () => {
                await client.destroy();
                process.exit();
            }, 1000);
        } else if (command === "eval") {
            response.edit("Evaluating...");
            try {
                // Evaluate (execute) our input
                const evaled = eval(args.join(" "));

                // Put our eval result through the function
                // we defined above
                const cleaned = await clean(evaled);

                // Reply in the channel with our result
                response.edit(`\`\`\`js\n${cleaned}\n\`\`\``);
            } catch (err) {
                // Reply in the channel with our error
                response.edit("an error occured: " + err);
            }
        } else {
            response.edit("Invalid command.");
            setTimeout(() => {
                response.delete();
            }, 3000);
        }
    }
}

const data = {
    name: Events.MessageCreate,
    once: false,
    execute
};

export { data };
