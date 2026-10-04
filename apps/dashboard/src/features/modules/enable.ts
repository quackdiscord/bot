/** ModuleKey names an optional module by its settings switch. */
export type ModuleKey = "tickets" | "general_logging" | "honeypot";

/** EnableProblem is why a module couldn't be switched on, ready to show. */
export type EnableProblem = {
  message: string;
  /** The module was never set up, so /setup in Discord is the fix. */
  needsSetup: boolean;
};

/**
 * enableProblem rewords a settings API refusal, such as switching on a
 * module that isn't set up. The API prefixes the real reason with "Guild
 * settings validation failed:", which says nothing useful to an admin, so
 * it is dropped.
 */
export function enableProblem(message: string): EnableProblem {
  const reason = message.replace(/^guild settings validation failed:\s*/i, "").trim();
  const needsSetup = /run \/setup/i.test(reason) && /not configured/i.test(reason);
  const sentence = reason.charAt(0).toUpperCase() + reason.slice(1);
  return {
    message: /[.!?]$/.test(sentence) ? sentence : `${sentence}.`,
    needsSetup,
  };
}
