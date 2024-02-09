import { Redis } from "@upstash/redis";
import config from "../config";

const redis = new Redis({
    url: config.redis.url as string,
    token: config.redis.token as string
});

export { redis };
