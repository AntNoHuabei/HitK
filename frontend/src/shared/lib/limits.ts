// Turn-budget limits, mirrored from internal/protocol/protocol.go.
//
// The generated Wails bindings carry types and methods but not Go constants,
// so these numbers are duplicated here. The backend clamps to the same values,
// so a drift would only make the input look more permissive than what actually
// gets saved — which is why the source of truth is named in one place.
export const DEFAULT_MAX_STEPS = 64;
export const MAX_STEPS_CEILING = 500;
