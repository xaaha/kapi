const ports = [];
let source = null;
let last = null;

onconnect = (e) => {
  const port = e.ports[0];
  ports.push(port);
  port.start();
  if (last) port.postMessage(last);
  if (!source) {
    source = new EventSource(new URL("../settings/events", location.href));
    source.onmessage = (m) => {
      last = m.data;
      for (const p of ports) p.postMessage(last);
    };
  }
};
