/** @file Fixed values shared across the UI. */

/** Base path of the forwarder to the service API (Go: internal/app/forward.go). */
export const ApiBase = "/hw";

/** Header that proves a request was made by this page (Go: internal/app/access.go). */
export const ClientHeader = "X-HW-Client";

/** How often the frame refreshes the connection status, in milliseconds. */
export const StatusPollMs = 5000;

/** How often an open thread checks for new messages, in milliseconds. */
export const ThreadPollMs = 5000;

/** How often the contact list (balances, limits) is refreshed, in milliseconds. */
export const ContactsPollMs = 60000;

/** Messages fetched per page of a thread. */
export const ThreadPageSize = 25;

/** The only audio container the service accepts for voice messages. */
export const VoiceExtension = ".m4a";

/** Content type the app declares for a voice part. */
export const VoiceContentType = "audio/m4a";

/** Distance from the bottom, in pixels, within which a thread keeps following new messages. */
export const FollowThresholdPx = 120;

/** How long a "Copied" confirmation stays visible, in milliseconds. */
export const CopiedNoticeMs = 1500;

/** Text length used when the facility's limit is missing from a contact. */
export const FallbackTextLimit = 160;
