let token, appId;

// optional if you want 2 different bots for dev and prod
if (process.env.ENVIORNMENT == "dev") {
    token = process.env.DEV_TOKEN;
    appId = process.env.DEV_APP_ID;
} else {
    token = process.env.TOKEN;
    appId = process.env.APP_ID;
}

const config = {
    token: token, // bot token from Discord
    appId: appId, // app ID from Discord
    mainColor: process.env.MAIN_COLOR, // main color of the bot (in hex)
    env: process.env.ENVIORNMENT, // environment (dev or prod)
    webhookUrl: process.env.WEBHOOK_URL, // webhook url (for private logging)
    mongoUrl: process.env.MONGO_URI, // mongo url for database
    event_api_url: process.env.EVENT_API_URL, // event api url (for events logging)
    event_api_token: process.env.EVENT_API_TOKEN, // event api token (to authenticate with the event api)

    redis: {
        url: process.env.REDIS_HOST,
        port: process.env.REDIS_PORT,
        password: process.env.REDIS_PASSWORD
    },

    database: {
        host: process.env.DATABASE_HOST,
        username: process.env.DATABASE_USERNAME,
        password: process.env.DATABASE_PASSWORD
    },

    axiom: {
        dataset: process.env.AXIOM_DATASET,
        token: process.env.AXIOM_TOKEN as string,
        orgId: process.env.AXIOM_ORG_ID
    },

    kafka: {
        brokers: [process.env.KAFKA_BROKER as string],
        username: process.env.KAFKA_USERNAME as string,
        password: process.env.KAFKA_PASSWORD as string
    }
};

export default config;
