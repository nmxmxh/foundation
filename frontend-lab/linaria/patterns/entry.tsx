import { renderToStaticMarkup } from "react-dom/server";
import { render, globals } from "./Patterns";
export const html = () => renderToStaticMarkup(render());
export { globals };
