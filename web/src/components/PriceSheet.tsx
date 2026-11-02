/** @file What each kind of message costs at a contact's facility. */

import type { Contact, MessagingEnabled, Prison } from "../types";
import { formatDollars, parseNumber } from "../util";
import { Notice } from "./ui";

/** Marks a price the service did not send. */
const Unknown = Number.NaN;

/** One line of the sheet. */
interface PriceRow {
	label: string;
	/** Dollars, or NaN when unknown. */
	price: number;
	/** What the price is per, such as "message". */
	unit: string;
	/** The messaging_enabled flag that applies, if any. */
	allowed?: keyof MessagingEnabled;
}

interface PriceSheetProps {
	contact: Contact;
	/** The facility, or null while it loads or when it failed. */
	prison: Prison | null;
	/** Why the facility could not be loaded, if it could not. */
	error: string;
	/** The latest balance, from the heartbeat when there is one. */
	balance: string | undefined;
}

/**
 * Builds the rows. The facility's messaging_price is the source; the two
 * prices on the contact row fill in when it has not loaded.
 * @param contact - The contact, for fallback prices.
 * @param prison - The facility, when loaded.
 * @returns The rows in the order the app's composer offers the kinds.
 */
function priceRows(contact: Contact, prison: Prison | null): PriceRow[] {
	const listed = prison?.messaging_price ?? {};
	const rows: PriceRow[] = [
		{ label: "Text", price: listed.text ?? parseNumber(contact.prison_price_per_text_message, Unknown), unit: "message", allowed: "text" },
		{ label: "Voice", price: parseNumber(prison?.price_per_voice_message_minute, Unknown), unit: "minute", allowed: "voice" },
		{ label: "Image", price: listed.image ?? Unknown, unit: "message", allowed: "image" },
		{ label: "GIF", price: listed.gif ?? Unknown, unit: "message", allowed: "gif" },
		{ label: "Video", price: listed.video ?? parseNumber(contact.prison_price_per_video_message, Unknown), unit: "message", allowed: "video" },
		{ label: "Library", price: listed.library ?? Unknown, unit: "message", allowed: "library" },
	];
	if (prison?.enable_emoji_price === "1") {
		rows.push({ label: "Emoji", price: parseNumber(prison.price_per_emoji_symbol, Unknown), unit: "emoji" });
	}
	return rows;
}

/** The price list behind the info button of a thread. */
export function PriceSheet({ contact, prison, error, balance }: PriceSheetProps) {
	const enabled = contact.messaging_enabled;
	return (
		<section class="border-b border-line bg-panel px-4 py-3 text-sm" aria-label="Prices">
			<h3 class="font-semibold">Prices at {prison?.name ?? contact.prison_name}</h3>
			<table class="mt-2 w-full max-w-sm">
				<tbody>
					{priceRows(contact, prison).map((row) => {
						const blocked = row.allowed !== undefined && enabled !== undefined && !enabled[row.allowed];
						return (
							<tr key={row.label} class={blocked ? "text-muted" : undefined}>
								<td class="py-0.5 pr-4">{row.label}</td>
								<td class="py-0.5 text-right tabular-nums">
									{blocked ? "not allowed" : Number.isNaN(row.price) ? "unknown" : `${formatDollars(row.price)} / ${row.unit}`}
								</td>
							</tr>
						);
					})}
				</tbody>
			</table>
			{balance && <p class="mt-2 text-muted">Balance: ${balance}</p>}
			{error && (
				<div class="mt-2">
					<Notice tone="warn">The facility's price list did not load ({error}). Only the prices on the contact are shown.</Notice>
				</div>
			)}
		</section>
	);
}
