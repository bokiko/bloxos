type Feedback = { type: "success" | "error" | "info"; message: string };
export function commandFeedback(httpOK: boolean, data: unknown, completedMessage: string, commandType?: string): Feedback;
export function bulkCommandFeedback(httpOK: boolean, data: unknown, expectedCount: number, commandType: string): Feedback;
export function selectionAfterBulkAttempt(current: Iterable<string>, attempted: Iterable<string>): Set<string>;
