import { showHome } from "./home.js";
import { showSidebar } from "./sidebar.js";

const home = document.getElementById("kapi-home");
if (home) showHome(home);
else showSidebar();
