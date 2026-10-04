import type { StatBucket } from "~/api/types";

/** fillDays turns sparse YYYY-MM-DD buckets into one entry per UTC day. */
export function fillDays(
  buckets: StatBucket[],
  from: Date,
  days: number,
): { day: string; count: number }[] {
  const counts = new Map(buckets.map((b) => [b.key, b.count]));
  const out: { day: string; count: number }[] = [];
  for (let i = 0; i < days; i++) {
    const day = new Date(from.getTime() + i * 86400_000).toISOString().slice(0, 10);
    out.push({ day, count: counts.get(day) ?? 0 });
  }
  return out;
}
