// One server-sent event stream per page: browsers allow only six HTTP/1.1
// connections per host, and every open stream holds one.
export const events = new EventSource("/api/events");
