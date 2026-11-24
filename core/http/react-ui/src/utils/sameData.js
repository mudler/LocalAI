// keepIfSame returns `prev` when `next` has the same JSON as it, so a state
// setter given (prev) => keepIfSame(prev, next) leaves React nothing to render
// when a poll answers with what the page already shows. The data of the API
// is plain JSON, and the server writes its keys in one order.
export function keepIfSame(prev, next) {
  try {
    return JSON.stringify(prev) === JSON.stringify(next) ? prev : next
  } catch {
    return next
  }
}
