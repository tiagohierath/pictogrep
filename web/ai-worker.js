import {
  AutoProcessor,
  AutoTokenizer,
  CLIPTextModelWithProjection,
  CLIPVisionModelWithProjection,
  RawImage,
} from "https://cdn.jsdelivr.net/npm/@huggingface/transformers@3.7.1/+esm";

const MODEL = "Xenova/clip-vit-base-patch32";
const MODEL_OPTIONS = { device: "wasm", dtype: "q8" };

let processorPromise;
let visionPromise;
let tokenizerPromise;
let textPromise;

function progress(detail) {
  self.postMessage({ type: "progress", detail });
}

function visionParts() {
  processorPromise ??= AutoProcessor.from_pretrained(MODEL, { progress_callback: progress });
  visionPromise ??= CLIPVisionModelWithProjection.from_pretrained(MODEL, {
    ...MODEL_OPTIONS,
    progress_callback: progress,
  });
  return Promise.all([processorPromise, visionPromise]);
}

function textParts() {
  tokenizerPromise ??= AutoTokenizer.from_pretrained(MODEL, { progress_callback: progress });
  textPromise ??= CLIPTextModelWithProjection.from_pretrained(MODEL, {
    ...MODEL_OPTIONS,
    progress_callback: progress,
  });
  return Promise.all([tokenizerPromise, textPromise]);
}

self.addEventListener("message", async event => {
  const { id, type } = event.data;
  try {
    if (type === "embed") {
      const [processor, model] = await visionParts();
      const image = await RawImage.fromURL(new URL(event.data.item.url, self.location.origin).href);
      const inputs = await processor(image);
      const { image_embeds } = await model(inputs);
      const vector = Array.from(image_embeds.normalize().data);
      self.postMessage({ type: "result", id, result: vector });
      return;
    }
    if (type === "search") {
      const [tokenizer, model] = await textParts();
      const inputs = tokenizer(event.data.query, { padding: true, truncation: true });
      const { text_embeds } = await model(inputs);
      const vector = Array.from(text_embeds.normalize().data);
      self.postMessage({ type: "result", id, result: vector });
      return;
    }
    throw new Error("Unknown AI request");
  } catch (error) {
    self.postMessage({ type: "error", id, error: error?.message || String(error) });
  }
});
