const ports = [];
let source = null;

onconnect = (e) => {
  const port = e.ports[0];
  ports.push(port);
  port.start();
  if (!source) {
    source = new EventSource(new URL("../../api/notebook/events", location.href));
    source.onmessage = () => {
      for (const p of ports) p.postMessage("changed");
    };
  }
  port.postMessage("changed");
};
