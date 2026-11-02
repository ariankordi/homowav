/** @file All network access of the UI: the local settings API and the service API. */

import { ApiBase, ClientHeader, MsPerSecond, ThreadPageSize, VoiceContentType } from "./constants";
import type { AppConfig, AppStatus, Contact, Heartbeat, Message, MessageInit, OutgoingMessage, Prison } from "./types";

/** The {ok, cause, body} wrapper both the service and the Go side use. */
interface Envelope<T> {
	ok?: boolean;
	cause?: string;
	body?: T;
}

/** A failed call, with the HTTP status so callers can tell "not paired" apart. */
export class ApiError extends Error {
	readonly status: number;

	constructor(message: string, status: number) {
		super(message);
		this.name = "ApiError";
		this.status = status;
	}
}

/** HTTP status the Go side answers with to a device that has not been paired. */
export const NotPairedStatus = 403;

/** Reads a reply as JSON, or returns null when it is something else. */
const parseJson = async (response: Response): Promise<unknown> => {
	try {
		return await response.json();
	} catch {
		return null;
	}
};

/**
 * Sends one request with the header that marks it as ours.
 * @param path - Path on this origin.
 * @param init - Fetch options.
 * @returns The decoded JSON reply.
 * @throws {ApiError} When the network fails or the status is not 2xx.
 */
async function request(path: string, init: RequestInit = {}): Promise<unknown> {
	const headers = new Headers(init.headers);
	headers.set(ClientHeader, "1");
	let response: Response;
	try {
		response = await fetch(path, { ...init, headers, credentials: "same-origin" });
	} catch {
		throw new ApiError("Cannot reach hwclient. Is it still running?", 0);
	}
	const decoded = await parseJson(response);
	if (!response.ok) {
		const cause = (decoded as Envelope<unknown> | null)?.cause;
		throw new ApiError(cause ?? `Request failed (HTTP ${response.status}).`, response.status);
	}
	return decoded;
}

/**
 * Calls the service API through the forwarder and unwraps the envelope.
 * @param path - Service path starting with "/api/".
 * @param init - Fetch options.
 * @returns The envelope's body.
 * @throws {ApiError} When the service answers ok=false.
 */
async function service<T>(path: string, init: RequestInit = {}): Promise<T> {
	const envelope = (await request(ApiBase + path, init)) as Envelope<T> | null;
	if (!envelope || envelope.ok !== true) {
		throw new ApiError(envelope?.cause ?? "The service gave an unexpected reply.", 200);
	}
	return envelope.body as T;
}

/** Local settings API (Go package app). */
export class LocalApi {
	static status(): Promise<AppStatus> {
		return request("/app/status") as Promise<AppStatus>;
	}

	static renew(): Promise<AppStatus> {
		return request("/app/renew", { method: "POST" }) as Promise<AppStatus>;
	}

	static config(): Promise<AppConfig> {
		return request("/app/config") as Promise<AppConfig>;
	}

	static saveConfig(config: Pick<AppConfig, "visitor_id" | "media">): Promise<AppConfig> {
		return request("/app/config", {
			method: "PUT",
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify(config),
		}) as Promise<AppConfig>;
	}

	/**
	 * Saves the port for the next start; 0 goes back to the default.
	 * @param port - The new port.
	 * @returns The settings after the change.
	 */
	static savePort(port: number): Promise<AppConfig> {
		return request("/app/port", {
			method: "PUT",
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify({ port }),
		}) as Promise<AppConfig>;
	}

	/**
	 * Signs in to the service with a password.
	 * @param username - The account's username.
	 * @param password - Empty reuses the saved password for the same username.
	 * @param save - Keep the login for automatic sign-ins; false forgets a saved one.
	 * @returns The status after signing in.
	 */
	static signIn(username: string, password: string, save: boolean): Promise<AppStatus> {
		return request("/app/account", {
			method: "POST",
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify({ username, password, save }),
		}) as Promise<AppStatus>;
	}

	/** Forgets the saved login; the current session stays. */
	static forgetAccount(): Promise<AppConfig> {
		return request("/app/account", { method: "DELETE" }) as Promise<AppConfig>;
	}

	/** Asks the program to stop and start again. */
	static async restart(): Promise<void> {
		await request("/app/restart", { method: "POST" });
	}

	/**
	 * Waits until a server at origin is reachable (or, with wantUp false,
	 * gone). The reply is opaque when origin is another port (no CORS), but
	 * whether one arrives is all this needs.
	 * @param origin - Such as "http://192.168.1.5:8123".
	 * @param wantUp - Wait for it to answer (true) or to stop answering (false).
	 * @param pollMs - Pause between tries.
	 * @param timeoutMs - When to give up.
	 * @returns Whether that happened in time.
	 */
	static async waitFor(origin: string, wantUp: boolean, pollMs: number, timeoutMs: number): Promise<boolean> {
		const deadline = Date.now() + timeoutMs;
		while (Date.now() < deadline) {
			await new Promise((resolve) => setTimeout(resolve, pollMs));
			let up = true;
			try {
				await fetch(`${origin}/app/status`, { mode: "no-cors", cache: "no-store" });
			} catch {
				up = false;
			}
			if (up === wantUp) {
				return true;
			}
		}
		return false;
	}

	/**
	 * Stores a file in the configured bucket.
	 * @param file - The file to upload.
	 * @returns Its public URL.
	 */
	static async upload(file: File): Promise<string> {
		const reply = (await request(`/app/upload?name=${encodeURIComponent(file.name)}`, {
			method: "POST",
			headers: { "Content-Type": file.type || "application/octet-stream" },
			body: file,
		})) as { url: string };
		return reply.url;
	}
}

/**
 * Gives a just-sent message the timestamp thread entries carry. The send reply
 * only has "created" in the facility's local time, with no zone to read it in,
 * and the message was created a moment ago anyway.
 */
const withTimestamp = (message: Message): Message =>
	typeof message.created_timestamp === "number"
		? message
		: { ...message, created_timestamp: Math.floor(Date.now() / MsPerSecond) };

/**
 * Facility details by id, fetched once per page load: prices do not change
 * while a conversation is open, and contacts often share a facility.
 */
const prisonCache = new Map<string, Promise<Prison>>();

/** The parts of the service API the UI uses. Shapes follow captures of the app. */
export class ServiceApi {
	/**
	 * A facility's details, including its message prices.
	 * @param prisonId - Contact.prison_id.
	 * @returns The facility; a failed fetch is not cached, so it is retried next time.
	 */
	static prison(prisonId: string): Promise<Prison> {
		let pending = prisonCache.get(prisonId);
		if (!pending) {
			pending = service<Prison>(`/api/app/prisons/${encodeURIComponent(prisonId)}`);
			pending.catch(() => prisonCache.delete(prisonId));
			prisonCache.set(prisonId, pending);
		}
		return pending;
	}

	static contacts(visitorId: string): Promise<Contact[]> {
		return service<Contact[]>("/api/app/inmates-by-visitor", {
			method: "POST",
			body: new URLSearchParams({ id: visitorId }),
		});
	}

	/** Presence and unread count, cheaper than reloading a thread. */
	static heartbeat(): Promise<Heartbeat> {
		return service<Heartbeat>("/api/visitor/heartbeat?ctx=mobile");
	}

	static thread(inmateId: string, page: number): Promise<Message[]> {
		const query = new URLSearchParams({ inmate_id: inmateId, page: String(page), limit: String(ThreadPageSize) });
		return service<Message[]>(`/api/app/visitor/chat?${query}`);
	}

	/**
	 * Sends one message: the service first hands out an id, then takes the message.
	 * @param contact - The recipient.
	 * @param visitorId - The sender's account id.
	 * @param outgoing - What to send.
	 * @returns The stored message.
	 */
	static async send(contact: Contact, visitorId: string, outgoing: OutgoingMessage): Promise<Message> {
		const initQuery = new URLSearchParams({ inmate_id: contact.id, visitor_id: visitorId });
		const init = await service<MessageInit>(`/api/message/init?${initQuery}`);
		const elapsed = String(outgoing.elapsedSeconds);

		if (outgoing.type === "voice" && outgoing.voice) {
			// The app sends a recording as multipart, without visitor_id or gallery_url.
			const form = new FormData();
			form.set("elapsed_time", elapsed);
			form.set("pubid", init.pubid);
			form.set("type", "voice");
			form.set("inmate_id", contact.id);
			form.set("sender", "visitor");
			form.set("body", "");
			form.set("voice", new File([outgoing.voice], outgoing.voice.name, { type: VoiceContentType }));
			return withTimestamp(await service<Message>("/api/message/send", { method: "POST", body: form }));
		}
		const fields = new URLSearchParams({
			body: outgoing.body,
			elapsed_time: elapsed,
			gallery_url: outgoing.galleryUrl,
			inmate_id: contact.id,
			pubid: init.pubid,
			sender: "visitor",
			type: outgoing.type,
			visitor_id: visitorId,
		});
		return withTimestamp(await service<Message>("/api/message/send", { method: "POST", body: fields }));
	}
}
