/** @file The logo, drawn inline so its wordmark can follow the theme. */

import { AppName } from "../constants";
import logoSvg from "../assets/logo.svg?raw";

/** Height of the logo in the header, in rem; the width follows its aspect ratio. */
const LogoHeightRem = 1.5;

/**
 * Renders assets/logo.svg inline. An <img> would not work: the wordmark is
 * painted with currentColor, which only inherits the page's text color when
 * the SVG is part of the document.
 */
export function Logo() {
	return (
		<span
			role="img"
			aria-label={AppName}
			class="inline-flex shrink-0 [&>svg]:h-full [&>svg]:w-auto"
			style={{ height: `${LogoHeightRem}rem` }}
			// The markup is our own bundled file, not user input.
			dangerouslySetInnerHTML={{ __html: logoSvg }}
		/>
	);
}
