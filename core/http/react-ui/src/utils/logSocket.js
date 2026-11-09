// Reconnect rule of the live log views. A socket that closes on its own is
// reopened after a delay. A socket the view closed itself, because it
// unmounted or switched to another log, must not be: its close event arrives
// after the cleanup, and a reconnect then would open a socket nobody owns.
//
//   closed   true once the view's cleanup ran
//   current  the socket the view holds now (null after cleanup)
//   ws       the socket whose close event fired
export function shouldReconnect({ closed, current, ws }) {
  return !closed && current === ws
}
