import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

// Fuentes autoalojadas: viajan dentro del bundle, así que la aplicación no
// depende de una CDN externa ni hace peticiones a terceros, y la tipografía
// carga igual sin conexión. Las variables traen todo el rango de pesos en un
// único archivo.
import "@fontsource-variable/bricolage-grotesque";
import "@fontsource-variable/public-sans";
import "@fontsource/ibm-plex-mono/400.css";
import "@fontsource/ibm-plex-mono/500.css";
import "@fontsource/ibm-plex-mono/600.css";

import App from "./App";
import "./styles/globals.css";

const rootElement = document.getElementById("root");

if (!rootElement) {
  throw new Error("No se encontró el elemento #root en el documento");
}

createRoot(rootElement).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
