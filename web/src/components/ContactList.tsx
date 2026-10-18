/** @file The list of people the account can message. */

import { contactHash } from "../route";
import type { Contact } from "../types";
import { classes } from "../util";

interface ContactListProps {
	contacts: Contact[];
	selectedId: string | null;
	/** Whether a contact is online; undefined until the first heartbeat. */
	online: (contactId: string) => boolean | undefined;
}

/** First letters of a name, for the avatar circle. */
const initials = (name: string): string =>
	name
		.split(/\s+/)
		.filter(Boolean)
		.slice(0, 2)
		.map((word) => word[0].toUpperCase())
		.join("");

interface PresenceDotProps {
	online: boolean | undefined;
}

/** A small dot on the avatar: filled when online, hollow when offline, absent when unknown. */
function PresenceDot({ online }: PresenceDotProps) {
	if (online === undefined) {
		return null;
	}
	return (
		<span
			title={online ? "Online" : "Offline"}
			class={classes(
				"absolute -bottom-0.5 -right-0.5 h-3.5 w-3.5 rounded-full border-2 border-panel",
				online ? "bg-good" : "bg-line",
			)}
		/>
	);
}

/** The chat participant list. Each row links to its conversation, so reload keeps it open. */
export function ContactList({ contacts, selectedId, online }: ContactListProps) {
	if (contacts.length === 0) {
		return <p class="p-4 text-sm text-muted">No contacts on this account yet. Add one in the official app.</p>;
	}
	return (
		<ul class="divide-y divide-line">
			{contacts.map((contact) => (
				<li key={contact.id}>
					<a
						href={contactHash(contact.id)}
						aria-current={contact.id === selectedId ? "page" : undefined}
						class={classes("flex w-full items-center gap-3 px-4 py-3 text-left hover:bg-bubble", contact.id === selectedId && "bg-bubble")}
					>
						<span class="relative flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-accent text-sm font-semibold text-accent-ink">
							{initials(contact.full_name)}
							<PresenceDot online={online(contact.id)} />
						</span>
						<span class="min-w-0">
							<span class="block truncate font-medium">{contact.full_name}</span>
							<span class="block truncate text-xs text-muted">{contact.prison_name}</span>
						</span>
					</a>
				</li>
			))}
		</ul>
	);
}
