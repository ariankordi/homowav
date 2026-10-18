/** @file The screen and open conversation, kept in the URL hash so reload and back work. */

/** Where the app is: the chat (optionally with one contact open) or Settings. */
export type Route = { screen: "chat"; contactId: string | null } | { screen: "settings" };

/** Hash of the Settings screen. */
export const SettingsHash = "#/settings";

/** Hash of the chat with no contact open. */
export const ChatHash = "#/";

/** Prefix of a conversation's hash; the contact id follows. */
const ContactHashPrefix = "#/chat/";

/** Contact ids are numbers; anything else in the hash is ignored. */
const ContactIdPattern = /^\d+$/;

/**
 * Reads a route from a hash.
 * @param hash - location.hash, including the "#".
 * @returns The route; unknown hashes are the chat list.
 */
export function parseRoute(hash: string): Route {
	if (hash === SettingsHash) {
		return { screen: "settings" };
	}
	if (hash.startsWith(ContactHashPrefix)) {
		const contactId = decodeURIComponent(hash.slice(ContactHashPrefix.length));
		if (ContactIdPattern.test(contactId)) {
			return { screen: "chat", contactId };
		}
	}
	return { screen: "chat", contactId: null };
}

/** The hash that opens a conversation. */
export const contactHash = (contactId: string): string => ContactHashPrefix + encodeURIComponent(contactId);
