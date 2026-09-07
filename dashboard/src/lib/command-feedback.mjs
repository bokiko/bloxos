// A command that disconnects its own agent cannot report completion. Keep
// "sent" distinct from "succeeded", including partial bulk failures.
export function commandFeedback(httpOK, data, completedMessage) {
  if (httpOK && data?.accepted === true) {
    return { type: "info", message: data.output || "Request sent; completion is not confirmed." };
  }
  if (httpOK && data?.success === true) {
    return { type: "success", message: completedMessage };
  }
  return { type: "error", message: `Failed: ${data?.error || data?.output || "unknown error"}` };
}

export function bulkCommandFeedback(httpOK, data, expectedCount, commandType) {
  const results = Array.isArray(data?.results) ? data.results : [];
  const accepted = results.filter(r => r.accepted === true).length;
  const succeeded = results.filter(r => !r.accepted && r.success === true).length;
  const isReboot = commandType === "reboot";
  const acknowledged = isReboot
    ? `${succeeded} acknowledged (reboot/recovery unconfirmed)`
    : `${succeeded} completed`;
  const failed = Math.max(expectedCount, results.length) - accepted - succeeded;
  if (!httpOK || failed > 0 || results.length === 0) {
    return { type: "error", message: `${acknowledged}, ${accepted} sent (unconfirmed), ${failed || expectedCount} failed. Check machine status before retrying.` };
  }
  return {
    type: accepted || isReboot ? "info" : "success",
    message: `${acknowledged}, ${accepted} sent${accepted ? " (completion unconfirmed; check machine status)" : ""}.`,
  };
}
