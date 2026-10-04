/** cx joins class names, skipping falsy ones. */
export function cx(...names: (string | false | null | undefined)[]): string {
  let out = "";
  for (const name of names) {
    if (name) out = out ? `${out} ${name}` : name;
  }
  return out;
}
