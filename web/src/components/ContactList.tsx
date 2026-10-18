/** @file The list of people the account can message. */

import { contactHash } from "../route";
import type { Contact } from "../types";
import { classes } from "../util";
import { Avatar } from "./ui";

interface ContactListProps {
	contacts: Contact[];
	selectedId: string | null;
	/** Whether a contact is online; undefined until the first heartbeat. */
	online: (contactId: string) => boolean | undefined;
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
						<Avatar name={contact.full_name} online={online(contact.id)} />
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
