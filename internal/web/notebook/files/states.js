const DEFINITIONS = Object.freeze({
  live: Object.freeze({ label: "active", group: "active", tone: "green", working: true }),
  waiting: Object.freeze({ label: "active", group: "active", tone: "yellow", working: true }),
  idle: Object.freeze({ label: "inactive", group: "inactive", tone: "grey", working: false }),
  ended: Object.freeze({ label: "ended", group: "ended", tone: "red", working: false }),
});

const UNKNOWN = Object.freeze({ label: "", group: "", tone: "grey", working: false });
const MISSING = Object.freeze({ label: "File moved or deleted", group: "ended", tone: "red", working: false });

export const STATUS_FILTERS = Object.freeze([
  Object.freeze({ value: "active", label: "Active" }),
  Object.freeze({ value: "inactive", label: "Inactive" }),
  Object.freeze({ value: "ended", label: "Ended" }),
  Object.freeze({ value: "", label: "All" }),
]);

export const stateInfo = (page) => page.missing ? MISSING : DEFINITIONS[page.state] ?? UNKNOWN;
export const stateLabel = (page) => stateInfo(page).label;
export const stateTone = (page) => stateInfo(page).tone;
export const stateMatches = (page, group) => !group || stateInfo(page).group === group;
export const isWorking = (page) => stateInfo(page).working;
