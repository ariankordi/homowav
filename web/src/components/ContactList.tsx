/** @file The list of people the account can message. */

import type { Contact } from "../types";
import { classes } from "../util";

interface ContactListProps {
	contacts: Contact[];
	selectedId: string | null;
	onSelect: (contact: Contact) => void;
}

/** First letters of a name, for the avatar circle. */
const initials = (name: string): string =>
	name
		.split(/\s+/)
		.filter(Boolean)
		.slice(0, 2)
		.map((word) => word[0].toUpperCase())
		.join("");

/** The chat participant list. */
export function ContactList({ contacts, selectedId, onSelect }: ContactListProps) {
	if (contacts.length === 0) {
		return <p class="p-4 text-sm text-muted">No contacts on this account yet. Add one in the official app.</p>;
	}
	return (
		<ul class="divide-y divide-line">
			{contacts.map((contact) => (
				<li key={contact.id}>
					<button
						type="button"
						onClick={() => onSelect(contact)}
						aria-current={contact.id === selectedId}
						class={classes("flex w-full items-center gap-3 px-4 py-3 text-left hover:bg-bubble", contact.id === selectedId && "bg-bubble")}
					>
						<span class="flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-accent text-sm font-semibold text-accent-ink">
							{initials(contact.full_name)}
						</span>
						<span class="min-w-0">
							<span class="block truncate font-medium">{contact.full_name}</span>
							<span class="block truncate text-xs text-muted">{contact.prison_name}</span>
						</span>
					</button>
				</li>
			))}
		</ul>
	);
}
