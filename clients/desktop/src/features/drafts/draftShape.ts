// An initial value is not a schema: variants may omit its keys and editable
// fields may deliberately contain RAW strings instead of typed server defaults.
// Only check the outer kind here; adapters validate their actual requirements.
export function matchesDraftContainer(value: unknown, initial: unknown): boolean {
  // A nonempty default also cannot tell us whether T permits clearing to null.
  if (value == null || initial == null) return true;
  if (Array.isArray(initial)) return Array.isArray(value);
  if (typeof initial === 'object') return !!value && typeof value === 'object' && !Array.isArray(value);
  return typeof value === typeof initial;
}
