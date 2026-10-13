/** @file The Settings screen: connection, phone setup, pairing, storage. */

import { useEffect, useState } from "preact/hooks";
import { LocalApi } from "../api";
import type { AppConfig, AppStatus, MediaConfig } from "../types";
import { formatIsoTime } from "../util";
import { QrCode } from "./QrCode";
import { Button, Card, CopyRow, Field, Notice } from "./ui";

/** Characters per group when the CA fingerprint is shown for comparison. */
const FingerprintGroup = 4;

/** A bucket form with nothing filled in. */
const EmptyMedia: MediaConfig = {
	endpoint: "",
	region: "",
	bucket: "",
	access_key_id: "",
	secret_access_key: "",
	public_base_url: "",
	key_prefix: "",
	path_style: false,
	acl: "",
};

interface SettingsProps {
	status: AppStatus;
	/** Replaces the frame's status after an action changed it. */
	onStatus: (status: AppStatus) => void;
}

interface SectionProps {
	status: AppStatus;
}

/** Splits a hex fingerprint into groups, so two displays can be compared by eye. */
const groupFingerprint = (hex: string): string => hex.toUpperCase().match(new RegExp(`.{1,${FingerprintGroup}}`, "g"))?.join(" ") ?? "";

/** The origin a LAN URL belongs to, for building the CA link next to it. */
const originOf = (url: string): string => new URL(url).origin;

/** Connection state and manual renewal. */
function ConnectionCard({ status, onStatus }: SettingsProps) {
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState("");
	const session = status.session;
	const renew = async () => {
		setBusy(true);
		setError("");
		try {
			onStatus(await LocalApi.renew());
		} catch (reason) {
			setError(reason instanceof Error ? reason.message : String(reason));
		} finally {
			setBusy(false);
		}
	};
	return (
		<Card title="Connection" hint={`The session shared with the phone app for ${status.api_host}.`}>
			{session.have_session ? <Notice tone="good">Connected.</Notice> : <Notice tone="warn">No session yet. Follow the phone setup below.</Notice>}
			<dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
				<dt class="text-muted">Session changed</dt>
				<dd>{formatIsoTime(session.session_updated)}</dd>
				<dt class="text-muted">Stays renewable until</dt>
				<dd>{session.can_renew ? formatIsoTime(session.remember_expires) : "not renewable yet"}</dd>
				<dt class="text-muted">Account id</dt>
				<dd>{status.visitor_id || "not seen yet"}</dd>
			</dl>
			<div class="flex items-center gap-3">
				<Button onClick={() => void renew()} disabled={busy || !session.can_renew}>
					{busy ? "Renewing…" : "Renew now"}
				</Button>
				<span class="text-xs text-muted">Renewal happens by itself. Use this only to check that it works.</span>
			</div>
			{error && <Notice tone="bad">{error}</Notice>}
		</Card>
	);
}

/** The three steps that connect a phone. */
function PhoneSetupCard({ status }: SectionProps) {
	const pacUrl = status.pac_urls[0];
	if (!pacUrl) {
		return (
			<Card title="Phone setup">
				<Notice tone="warn">This computer has no local network address. Connect it to the same Wi-Fi as the phone.</Notice>
			</Card>
		);
	}
	const caUrl = status.ca_url ? originOf(pacUrl) + status.ca_url : "";
	const connected = status.session.have_session && status.session.can_renew;
	return (
		<Card title="Phone setup" hint="Do this once per phone. The phone must be on the same Wi-Fi as this computer.">
			<ol class="list-decimal space-y-5 pl-5 text-sm">
				<li>
					<p class="font-medium">Trust this computer's certificate</p>
					{caUrl ? (
						<div class="mt-2 space-y-2">
							<p class="text-muted">Open this link in Safari on the phone and allow the profile download.</p>
							<QrCode text={caUrl} label="QR code of the certificate link" />
							<CopyRow value={caUrl} />
							<p class="text-muted">
								Then install it under Settings › General › VPN &amp; Device Management, and switch it on under Settings › General › About › Certificate Trust
								Settings.
							</p>
							<p class="text-xs text-muted">Fingerprint (SHA-256): {groupFingerprint(status.ca_fingerprint)}</p>
						</div>
					) : (
						<p class="mt-2 text-muted">This instance was started with its own certificate. Install the authority that issued it on the phone.</p>
					)}
				</li>
				<li>
					<p class="font-medium">Point the phone's Wi-Fi at the proxy</p>
					<div class="mt-2 space-y-2">
						<p class="text-muted">Settings › Wi-Fi › (your network) › Configure Proxy › Automatic, and enter this URL. Only {status.api_host} is routed here.</p>
						<CopyRow value={pacUrl} />
						{status.pac_urls.length > 1 && <p class="text-xs text-muted">Other addresses of this computer: {status.pac_urls.slice(1).join(", ")}</p>}
					</div>
				</li>
				<li>
					<p class="font-medium">Open the HomeWAV app once</p>
					<div class="mt-2">
						{connected ? <Notice tone="good">Done: the app's session was captured.</Notice> : <Notice tone="info">Waiting for the app to connect through the proxy…</Notice>}
					</div>
				</li>
			</ol>
		</Card>
	);
}

/** The link that admits another device's browser. */
function PairingCard({ status }: SectionProps) {
	const pairingUrl = status.pairing_urls[0];
	if (!pairingUrl) {
		return null;
	}
	return (
		<Card title="Use it from the phone" hint="Open this link in the phone's browser to use this screen from there. Treat it like a password: it gives access to the account.">
			<QrCode text={pairingUrl} label="QR code of the pairing link" />
			<CopyRow value={pairingUrl} />
		</Card>
	);
}

interface StorageCardProps {
	config: AppConfig;
	onSaved: (config: AppConfig) => void;
}

/** The upload bucket and the account id override. */
function StorageCard({ config, onSaved }: StorageCardProps) {
	const [media, setMedia] = useState<MediaConfig>({ ...EmptyMedia, ...config.media });
	const [visitorId, setVisitorId] = useState(config.visitor_id);
	const [busy, setBusy] = useState(false);
	const [saved, setSaved] = useState(false);
	const [error, setError] = useState("");
	const set = (field: keyof MediaConfig) => (value: string) => {
		setSaved(false);
		setMedia((previous) => ({ ...previous, [field]: value }));
	};
	const save = async (event: Event) => {
		event.preventDefault();
		setBusy(true);
		setError("");
		try {
			const next = await LocalApi.saveConfig({ visitor_id: visitorId, media });
			onSaved(next);
			setMedia({ ...EmptyMedia, ...next.media });
			setSaved(true);
		} catch (reason) {
			setError(reason instanceof Error ? reason.message : String(reason));
		} finally {
			setBusy(false);
		}
	};
	return (
		<Card title="Media storage" hint="Pictures and videos are sent as a public link. Fill this in to upload files from the composer to your own S3-compatible bucket.">
			<form onSubmit={save} class="space-y-3">
				<div class="grid gap-3 sm:grid-cols-2">
					<Field label="Bucket" value={media.bucket} onInput={set("bucket")} />
					<Field label="Region" value={media.region} onInput={set("region")} placeholder="us-east-1" />
					<Field label="Access key ID" value={media.access_key_id} onInput={set("access_key_id")} />
					<Field
						label="Secret access key"
						type="password"
						value={media.secret_access_key}
						onInput={set("secret_access_key")}
						placeholder={config.media_secret_set ? "Saved. Leave empty to keep it." : ""}
					/>
					<Field label="Endpoint" type="url" value={media.endpoint} onInput={set("endpoint")} placeholder="Empty for Amazon S3" hint="Only for other providers, such as R2 or MinIO." />
					<Field label="Public base URL" type="url" value={media.public_base_url} onInput={set("public_base_url")} placeholder="Empty to use the bucket's own address" hint="What recipients load files from." />
					<Field label="Key prefix" value={media.key_prefix} onInput={set("key_prefix")} placeholder="hw/" />
					<Field label="ACL" value={media.acl} onInput={set("acl")} placeholder="public-read" hint="Leave empty if the bucket is public by policy." />
				</div>
				<label class="flex items-center gap-2 text-sm">
					<input
						type="checkbox"
						checked={media.path_style}
						onChange={(event) => {
							setSaved(false);
							setMedia({ ...media, path_style: event.currentTarget.checked });
						}}
					/>
					Path-style addressing (needed by MinIO and some providers)
				</label>
				<Field
					label="Account id override"
					inputMode="numeric"
					value={visitorId}
					onInput={(value) => {
						setSaved(false);
						setVisitorId(value);
					}}
					placeholder="Empty to use the id seen from the app"
					hint="Only needed if the contact list stays empty."
				/>
				<div class="flex items-center gap-3">
					<Button kind="primary" type="submit" disabled={busy}>
						{busy ? "Saving…" : "Save"}
					</Button>
					{saved && <Notice tone="good">Saved.</Notice>}
				</div>
				{error && <Notice tone="bad">{error}</Notice>}
			</form>
		</Card>
	);
}

/** The Settings screen. */
export function Settings({ status, onStatus }: SettingsProps) {
	const [config, setConfig] = useState<AppConfig | null>(null);
	const [error, setError] = useState("");
	useEffect(() => {
		LocalApi.config()
			.then(setConfig)
			.catch((reason: unknown) => setError(reason instanceof Error ? reason.message : String(reason)));
	}, []);
	return (
		<div class="mx-auto max-w-3xl space-y-4 p-4">
			<ConnectionCard status={status} onStatus={onStatus} />
			<PhoneSetupCard status={status} />
			<PairingCard status={status} />
			{config && <StorageCard config={config} onSaved={setConfig} />}
			{error && <Notice tone="bad">{error}</Notice>}
		</div>
	);
}
