// Minimal Inertia stub so the fixture type-checks hermetically (no @inertiajs
// dependency). It is NOT under scan discrimination — it defines the render API
// the controller and the fix use.
export const Inertia = {
	render(component: string, props: Record<string, unknown>) {
		return { component, props };
	},
};
