/** @file Renders text as a QR code, for moving a URL to the phone. */

import qrcode from "qrcode-generator";
import { useMemo } from "preact/hooks";

/** Quiet zone around the code, in modules, as the QR standard asks for. */
const QuietZoneModules = 4;

/** Rendered size in CSS pixels. */
const SizePx = 176;

/** Type number 0 lets the library pick the smallest code that fits. */
const AutoTypeNumber = 0;

interface QrCodeProps {
	text: string;
	/** Description for screen readers. */
	label: string;
}

/**
 * Builds the SVG path for the dark modules of a QR code.
 * @param text - The content to encode.
 * @returns The path data and the side length in modules, quiet zone included.
 */
function qrPath(text: string): [path: string, side: number] {
	const code = qrcode(AutoTypeNumber, "M");
	code.addData(text);
	code.make();
	const count = code.getModuleCount();
	let path = "";
	for (let row = 0; row < count; row++) {
		for (let column = 0; column < count; column++) {
			if (code.isDark(row, column)) {
				path += `M${column + QuietZoneModules},${row + QuietZoneModules}h1v1h-1z`;
			}
		}
	}
	return [path, count + QuietZoneModules * 2];
}

/** A QR code. It is always black on white, whatever the theme: scanners expect that. */
export function QrCode({ text, label }: QrCodeProps) {
	const [path, side] = useMemo(() => qrPath(text), [text]);
	return (
		<svg role="img" aria-label={label} viewBox={`0 0 ${side} ${side}`} width={SizePx} height={SizePx} class="rounded-lg" shape-rendering="crispEdges">
			<rect width={side} height={side} fill="#ffffff" />
			<path d={path} fill="#000000" />
		</svg>
	);
}
