export function screenshotRouteMatches(requestedPath, finalPath, visitorPrefix) {
  if (finalPath === visitorPrefix || ['/', '?'].some((c) => finalPath.startsWith(visitorPrefix + c))) return true;
  if (visitorPrefix !== '/account') return false;

  const orderAlias = /^\/account\/orders\/([^/?#]+)(?:\?[^#]*)?$/.exec(requestedPath);
  return orderAlias !== null && finalPath === `/orders/${orderAlias[1]}`;
}
