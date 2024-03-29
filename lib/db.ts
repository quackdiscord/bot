// import { MongoClient, ServerApiVersion } from 'mongodb';
import { drizzle } from "drizzle-orm/mysql2";
import mysql from "mysql2/promise";
import config from "../config";

// create the connection
const connection = await mysql.createConnection({
    uri: config.db.uri
});

const db = drizzle(connection);

export { db };
