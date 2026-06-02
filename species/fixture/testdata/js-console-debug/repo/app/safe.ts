// safe.ts is the NON-MATCHING discrimination case: console.error is legitimate
// logging, not throwaway debug, so js-console-debug must NOT flag it (the rule
// matches only console.log / console.debug / debugger).
export function warnOnce(msg: string): void {
	console.error("non-debug, must be preserved:", msg);
}
