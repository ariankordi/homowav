import preact from "@preact/preset-vite";
import tailwindcss from "@tailwindcss/vite";
import { defineConfig } from "vite";

/** Where "go run ./cmd/hwclient-dev" listens; the dev server forwards the API there. */
const DevBackend = "http://127.0.0.1:8090";

/** Paths served by the Go side rather than by Vite. */
const BackendPaths = ["/hw", "/app", "/ca.pem", "/pair", "/proxy.pac"];

export default defineConfig({
	plugins: [preact(), tailwindcss()],
	build: { outDir: "dist", emptyOutDir: true },
	server: {
		proxy: Object.fromEntries(BackendPaths.map((path) => [path, DevBackend])),
	},
});
