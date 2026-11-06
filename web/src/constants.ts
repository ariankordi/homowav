/** @file Fixed values shared across the UI. */

/**
 * The name users see. The Go side has its own copy (Name in
 * internal/app/app.go) for the tray and the CA certificate.
 */
export const AppName = "homowav";

/** The README section on phone mode, which covers what Android needs. */
export const PhoneModeReadmeUrl = "https://github.com/ariankordi/homowav#phone-mode";

/** Base path of the forwarder to the service API (Go: internal/app/forward.go). */
export const ApiBase = "/hw";

/** Header that proves a request was made by this page (Go: internal/app/access.go). */
export const ClientHeader = "X-HW-Client";

/** How often the frame refreshes the connection status, in milliseconds. */
export const StatusPollMs = 5000;

/** Choices for how often to check for new messages; 0 means only on request. */
export const CheckIntervals = [
	{ label: "Every 3 s", ms: 3000 },
	{ label: "Every 5 s", ms: 5000 },
	{ label: "Every 10 s", ms: 10000 },
	{ label: "Every 30 s", ms: 30000 },
	{ label: "Every 1 min", ms: 60000 },
	{ label: "Manual", ms: 0 },
] as const;

/** The interval value that turns automatic checks off. */
export const ManualCheck = 0;

/** The official app polls its heartbeat about every ten seconds. */
export const DefaultCheckMs = 10000;

/**
 * The heartbeat only reports the unread count, which says nothing about our
 * own messages going from pending to approved to read. Every this many checks
 * the open thread is reloaded anyway.
 */
export const ThreadReloadEveryChecks = 6;

/** How often the contact list (limits, prices) is refreshed, in milliseconds. */
export const ContactsPollMs = 60000;

/** Heartbeat value of a contact who is online; anything else is offline. */
export const OnlineColor = "green";

/** localStorage key for the chosen check interval. */
export const CheckIntervalKey = "hwclient.checkIntervalMs";

/** Messages fetched per page of a thread. */
export const ThreadPageSize = 25;

/** The only audio container the service accepts for voice messages. */
export const VoiceExtension = ".m4a";

/** Content type the app declares for a voice part. */
export const VoiceContentType = "audio/m4a";

/** Milliseconds per second, for converting the service's unix timestamps. */
export const MsPerSecond = 1000;

/** Distance from the bottom, in pixels, within which a thread keeps following new messages. */
export const FollowThresholdPx = 120;

/** How long a "Copied" confirmation stays visible, in milliseconds. */
export const CopiedNoticeMs = 1500;

/** Text length used when the facility's limit is missing from a contact. */
export const FallbackTextLimit = 160;

/** The port used when none is saved (Go: app.DefaultListen). */
export const DefaultPort = 8080;

/** Valid range of a saved port (Go: app.MinPort, app.MaxPort). */
export const MinPort = 1;
export const MaxPort = 65535;

/** How often, and how long, the page checks for the restarted copy. */
export const RestartPollMs = 500;
export const RestartWaitMs = 20000;
