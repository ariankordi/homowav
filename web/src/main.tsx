/** @file Entry point: mounts the app. */

import { render } from "preact";
import { App } from "./app";
import { AppName } from "./constants";
import "./index.css";
import { requireElementById } from "./util";

// index.html carries a fallback title; the name lives in constants.ts.
document.title = AppName;
render(<App />, requireElementById("app"));
