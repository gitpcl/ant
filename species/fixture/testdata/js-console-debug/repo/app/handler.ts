// handler.ts holds exactly ONE js-console-debug finding (the console.log on the
// line below). detector-clears matches on species+file, so a fixture file must
// carry a single finding of the species — hence one debug statement per file.
// console.error in safe.ts is intentionally NOT a finding (legitimate logging).
export function handle(name: string): string {
	console.log("DEBUG: handle called with", name);
	return greet(name);
}

function greet(who: string): string {
	return `hello, ${who}`;
}
