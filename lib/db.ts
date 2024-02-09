// import { MongoClient, ServerApiVersion } from 'mongodb';
import { logger } from "./logger";
import { drizzle } from "drizzle-orm/planetscale-serverless";
import { connect } from "@planetscale/database";
import config from "../config";
import { MongoClient, ServerApiVersion } from "mongodb";

function connectDb() {
    try {
        const mClient = new MongoClient(process.env.MONGO_URI as string, {
            serverApi: ServerApiVersion.v1
        });

        mClient.connect().then(() => {
            logger.info("Connected to MongoDB.");
        });

        return mClient;
    } catch (error) {
        logger.error(error);
    }
}

// export { connectDb };

// create the connection
const connection = connect({
    host: config.database.host,
    username: config.database.username,
    password: config.database.password
});

const db = drizzle(connection);

export { db, connectDb };
