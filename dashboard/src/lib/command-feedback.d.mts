type Feedback = { type: "success" | "error" | "info"; message: string };
export function commandFeedback(httpOK: boolean, data: unknown, completedMessage: string): Feedback;
export function bulkCommandFeedback(httpOK: boolean, data: unknown, expectedCount: number): Feedback;
