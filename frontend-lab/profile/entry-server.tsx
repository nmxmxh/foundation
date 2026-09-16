import { renderToString } from "react-dom/server";
import { Feed, feedOptions } from "./feed";

/** Build-time render of the lab feed, for prerenderShell (frontend-kit/ts/vite). */
export function render(url: string): string {
  return renderToString(<Feed {...feedOptions(new URL(url, "http://prerender").search)} />);
}
