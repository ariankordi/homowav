/** @file Shapes of the data the UI exchanges with the Go side and the service. */

/** Connection state, as reported by GET /app/status (Go: session.Status). */
export interface SessionStatus {
	have_session: boolean;
	session_fingerprint: string;
	session_updated?: string;
	session_validated?: string;
	can_renew: boolean;
	remember_expires?: string;
	have_jwt: boolean;
	jwt_expires?: string;
	jwt_expired: boolean;
	renew_attempts: number;
	visitor_id: string;
}

/** Everything the frame needs (Go: app.statusReply). */
export interface AppStatus {
	session: SessionStatus;
	visitor_id: string;
	api_host: string;
	pac_urls: string[];
	pairing_urls: string[];
	ca_url: string;
	ca_fingerprint: string;
	media_configured: boolean;
}

/** Upload bucket settings (Go: app.Media). */
export interface MediaConfig {
	endpoint: string;
	region: string;
	bucket: string;
	access_key_id: string;
	secret_access_key: string;
	public_base_url: string;
	key_prefix: string;
	path_style: boolean;
	acl: string;
}

/** Settings as the page sees them (Go: app.configReply). */
export interface AppConfig {
	visitor_id: string;
	media: MediaConfig;
	media_secret_set: boolean;
}

/** Which message kinds a facility allows for a contact. */
export interface MessagingEnabled {
	video: boolean;
	library: boolean;
	image: boolean;
	gif: boolean;
	text: boolean;
	voice: boolean;
}

/**
 * One row of /api/app/inmates-by-visitor. The service sends about seventy
 * fields; only the ones the UI reads are listed. Numbers arrive as strings.
 */
export interface Contact {
	id: string;
	visitor_id: string;
	full_name: string;
	prison_name: string;
	relationship?: string;
	credit_balance?: string;
	bonus_balance?: string;
	prison_price_per_text_message?: string;
	prison_price_per_video_message?: string;
	prison_max_text_message_length?: string;
	prison_max_voice_message_length?: string;
	messages_disabled?: string | null;
	chat_disabled?: string | null;
	messaging_enabled?: MessagingEnabled;
}

/** Kinds of message the service knows. */
export type MessageType = "text" | "voice" | "gif" | "image" | "video";

/** One message of /api/app/visitor/chat. */
export interface Message {
	id: string;
	pubid: string;
	inmate_id: string;
	sender: "visitor" | "inmate";
	body: string;
	type: MessageType;
	status: string;
	created: string;
	/**
	 * Unix seconds. Thread entries have it; the reply to a send does not
	 * (api.ts fills it in), so code reading a Message can rely on it.
	 */
	created_timestamp: number;
	sender_name?: string;
	/** A URL for media, false otherwise. */
	stream_url?: string | false | null;
	image_url?: string | null;
}

/**
 * Reply of GET /api/visitor/heartbeat (the app polls it with ctx=mobile).
 * Maps are keyed by contact id.
 */
export interface Heartbeat {
	/** "green" when the contact is online, "red" otherwise. */
	status: Record<string, string>;
	is_occupant_suspended?: Record<string, boolean>;
	/** Count of unread messages from contacts, as a string. */
	unread_messages_count: string;
	credit_balances?: Record<string, string>;
}

/** Reply of /api/message/init. */
export interface MessageInit {
	pubid: string;
}

/** What the composer hands over to be sent. */
export interface OutgoingMessage {
	type: MessageType;
	/** Text of a text message; empty for media. */
	body: string;
	/** Public URL of a gif, image or video; empty otherwise. */
	galleryUrl: string;
	/** The recording of a voice message. */
	voice: File | null;
	/** Length reported to the service, in seconds. */
	elapsedSeconds: number;
}
