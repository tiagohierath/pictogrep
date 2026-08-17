import {
  AutoProcessor,
  AutoTokenizer,
  CLIPTextModelWithProjection,
  CLIPVisionModelWithProjection,
  RawImage,
  env,
} from "./transformers.web.min.js";

env.backends.onnx.wasm.wasmPaths = "/assets/";

const embeddingBackends = {
  clip: createCLIPBackend,
};

let activeModelKey = "";
let activeBackend = null;

function progress(kind) {
  return detail => self.postMessage({type: "progress", kind, detail});
}

function validateModel(model) {
  if (!model || typeof model.key !== "string" || typeof model.backend !== "string" ||
      typeof model.modelId !== "string" || typeof model.revision !== "string" ||
      typeof model.dtype !== "string" || !Number.isInteger(model.dimensions) ||
      model.dimensions < 1 || model.dimensions > 1 << 20) {
    throw new Error("Invalid embedding model configuration");
  }
  return model;
}

function backendFor(model) {
  model = validateModel(model);
  if (activeBackend) {
    if (model.key !== activeModelKey) throw new Error("Embedding model changed; reload Pictogrep");
    return activeBackend;
  }
  const createBackend = embeddingBackends[model.backend];
  if (!createBackend) throw new Error(`Unsupported embedding backend: ${model.backend}`);
  activeModelKey = model.key;
  activeBackend = createBackend(model);
  return activeBackend;
}

// Indexing reads Pictogrep's preview because the model only ever sees a small
// square, and decoding a whole photograph to reach it is the slowest part of
// preparing a library. Pictures with dimensions Pictogrep refuses to resize have
// no preview, so the original stays as the fallback rather than dropping them
// out of search entirely.
async function loadPicture(item) {
  const sources = [item.url, item.originalUrl].filter(Boolean);
  let failure = new Error("Picture has no source to read");
  for (const source of sources) {
    try {
      return await RawImage.fromURL(new URL(source, self.location.origin).href);
    } catch (error) {
      failure = error;
    }
  }
  throw failure;
}

function normalizedVector(tensor, dimensions) {
  const vector = Array.from(tensor.normalize().data);
  if (vector.length !== dimensions || vector.some(value => !Number.isFinite(value))) {
    throw new Error("Embedding model returned an invalid vector");
  }
  return vector;
}

function createCLIPBackend(model) {
  const options = {device: "wasm", dtype: model.dtype, revision: model.revision};
  let processorPromise;
  let visionPromise;
  let tokenizerPromise;
  let textPromise;

  async function visionParts() {
    try {
      processorPromise ??= AutoProcessor.from_pretrained(model.modelId, {
        revision: model.revision,
        progress_callback: progress("image"),
      });
      visionPromise ??= CLIPVisionModelWithProjection.from_pretrained(model.modelId, {
        ...options,
        progress_callback: progress("image"),
      });
      return await Promise.all([processorPromise, visionPromise]);
    } catch (error) {
      // A temporary network/cache failure must not poison every later attempt.
      processorPromise = undefined;
      visionPromise = undefined;
      throw error;
    }
  }

  async function textParts() {
    try {
      tokenizerPromise ??= AutoTokenizer.from_pretrained(model.modelId, {
        revision: model.revision,
        progress_callback: progress("text"),
      });
      textPromise ??= CLIPTextModelWithProjection.from_pretrained(model.modelId, {
        ...options,
        progress_callback: progress("text"),
      });
      return await Promise.all([tokenizerPromise, textPromise]);
    } catch (error) {
      tokenizerPromise = undefined;
      textPromise = undefined;
      throw error;
    }
  }

  return {
    async warmText() {
      await textParts();
    },

    async embedImage(item) {
      const [processor, visionModel] = await visionParts();
      try {
        const image = await loadPicture(item);
        const inputs = await processor(image);
        const {image_embeds: embedding} = await visionModel(inputs);
        return normalizedVector(embedding, model.dimensions);
      } catch (error) {
        const imageError = error instanceof Error ? error : new Error(String(error));
        imageError.kind = "image";
        throw imageError;
      }
    },

    async embedText(query) {
      const [tokenizer, textModel] = await textParts();
      const inputs = tokenizer(query, {padding: true, truncation: true});
      const {text_embeds: embedding} = await textModel(inputs);
      return normalizedVector(embedding, model.dimensions);
    },
  };
}

self.addEventListener("message", async event => {
  const {id, type} = event.data;
  try {
    const backend = backendFor(event.data.model);
    if (type === "health") {
      self.postMessage({type: "result", id, result: {model: activeModelKey}});
      return;
    }
    if (type === "warmText") {
      await backend.warmText();
      self.postMessage({type: "result", id, result: true});
      return;
    }
    if (type === "embed") {
      const vector = await backend.embedImage(event.data.item);
      self.postMessage({type: "result", id, result: vector});
      return;
    }
    if (type === "search") {
      const vector = await backend.embedText(event.data.query);
      self.postMessage({type: "result", id, result: vector});
      return;
    }
    throw new Error("Unknown AI request");
  } catch (error) {
    self.postMessage({type: "error", id, kind: error?.kind || "model", error: error?.message || String(error)});
  }
});
