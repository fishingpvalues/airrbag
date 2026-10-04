// Airrbag dashboard. Served by the Go binary at /__airrbag/ behind each *Arr,
// styled after the Sonarr/Radarr UI so it reads as part of the stack.

import { render } from "preact";
import { App } from "./app";
import "./styles.css";

const root = document.getElementById("app");
if (root) render(<App />, root);
