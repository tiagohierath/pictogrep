import {resolve} from "node:path";
import {pathToFileURL} from "node:url";
import {Worker} from "node:worker_threads";

const model = {
  key: "clip-vit-base-patch32-q8-v1",
  backend: "clip",
  modelId: "Xenova/clip-vit-base-patch32",
  revision: "d15189d7028b43f1d3e65039190477f6af591c2a",
  dtype: "q8",
  dimensions: 512,
};
const workerModule = pathToFileURL(resolve("web/ai-worker.js")).href;
const source = `
  import {parentPort} from "node:worker_threads";
  globalThis.self = {
    location: new URL("http://127.0.0.1/"),
    postMessage: message => parentPort.postMessage(message),
    addEventListener: (_type, listener) => parentPort.on("message", data => listener({data})),
  };
  // Exercise the browser code path even though this smoke test is hosted by Node.
  globalThis.process = undefined;
  await import(${JSON.stringify(workerModule)});
  parentPort.postMessage({type: "loaded"});
`;

const worker = new Worker(source, {eval: true, type: "module"});
const timeout = setTimeout(() => {
  console.error("AI worker did not start");
  process.exitCode = 1;
  worker.terminate();
}, 10_000);

worker.on("error", error => {
  clearTimeout(timeout);
  console.error(error);
  process.exitCode = 1;
});
worker.on("message", message => {
  if (message.type === "loaded") {
    worker.postMessage({id: 1, type: "health", model});
    return;
  }
  if (message.id !== 1) return;
  clearTimeout(timeout);
  if (message.type !== "result" || message.result?.model !== model.key) {
    console.error("AI worker rejected its default embedding model", message);
    process.exitCode = 1;
  }
  worker.terminate();
});
