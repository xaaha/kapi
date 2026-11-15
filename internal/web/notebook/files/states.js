const DEFINITIONS = Object.freeze({
  live: Object.freeze({ label: "active", tone: "green", working: true }),
  waiting: Object.freeze({ label: "active", tone: "yellow", working: true }),
  idle: Object.freeze({ label: "inactive", tone: "grey", working: false }),
  ended: Object.freeze({ label: "ended", tone: "red", working: false }),
});

const UNKNOWN = Object.freeze({ label: "", tone: "grey", working: false });
const MISSING = Object.freeze({ label: "File moved or deleted", tone: "red", working: false });

export const stateInfo = (page) => page.missing ? MISSING : DEFINITIONS[page.state] ?? UNKNOWN;
export const stateLabel = (page) => stateInfo(page).label;
export const stateTone = (page) => stateInfo(page).tone;
export const isWorking = (page) => stateInfo(page).working;
