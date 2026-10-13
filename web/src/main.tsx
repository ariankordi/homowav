/** @file Entry point: mounts the app. */

import { render } from "preact";
import { App } from "./app";
import "./index.css";
import { requireElementById } from "./util";

render(<App />, requireElementById("app"));
