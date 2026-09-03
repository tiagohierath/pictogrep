package main

import (
	"math"
	"strings"
	"unicode"
)

// A month heading says how many pictures landed but nothing about what they
// were. The theme line answers that in a few words: "Soft concept art,
// character portraits". It is read straight off the pictures themselves,
// using the same CLIP vectors AI search already stores, so nothing is sent
// anywhere and no extra model is downloaded.
//
// Each option carries the phrase the model is asked about and the shorter
// words shown to the reader. The phrase is worded the way a search would be
// typed, because that is what these vectors were trained against, while the
// label has to sit under a heading without reading like a prompt.
type themeOption struct {
	Label   string
	LabelPT string
	Prompt  string
}

// The phrase asked of the model always stays English, because that is the
// language CLIP was trained in; only what is shown changes.
func (option themeOption) label(language string) string {
	if language == "pt-BR" && option.LabelPT != "" {
		return option.LabelPT
	}
	return option.Label
}

type themeFacet struct {
	Options []themeOption
}

// Two facets, so a theme names a way of drawing and then what was drawn.
// Picking the best of each keeps the two halves from saying the same thing
// twice, which is what a single ranked list of forty phrases always does.
var themeFacets = []themeFacet{
	{Options: []themeOption{
		{"Soft concept art", "Concept art suave", "soft painterly concept art with gentle light"},
		{"Concept art", "Concept art", "digital concept art painting for a game or film"},
		{"Character design", "Design de personagem", "a character design sheet with turnarounds"},
		{"Anime art", "Arte anime", "anime and manga style illustration"},
		{"Cartoon art", "Arte cartoon", "cartoon and animation style illustration"},
		{"Comics", "Quadrinhos", "a comic book page with panels and speech bubbles"},
		{"Oil painting", "Pintura a óleo", "a classical oil painting on canvas"},
		{"Watercolour", "Aquarela", "a loose watercolour painting on paper"},
		{"Ink drawing", "Desenho a nanquim", "a black ink drawing with strong linework"},
		{"Pencil sketches", "Esboços a lápis", "a rough pencil sketch on paper"},
		{"3D renders", "Renders 3D", "a 3d rendered model with studio lighting"},
		{"Pixel art", "Pixel art", "pixel art with visible square pixels"},
		{"Graphic design", "Design gráfico", "a graphic design poster with big type"},
		{"Photography", "Fotografia", "a photograph taken with a camera"},
		{"Film stills", "Cenas de cinema", "a cinematic still frame from a film"},
		{"Fashion photos", "Fotos de moda", "a fashion photograph of an outfit on a model"},
	}},
	{Options: []themeOption{
		{"portraits", "retratos", "a close portrait of a person's face"},
		{"women and girls", "mulheres e garotas", "a young woman as the main subject"},
		{"men and boys", "homens e garotos", "a man as the main subject"},
		{"war and armour", "guerra e armaduras", "soldiers, armour and weapons"},
		{"monsters", "monstros", "a monster or strange creature"},
		{"fantasy scenes", "cenas de fantasia", "a fantasy scene with magic and myth"},
		{"science fiction", "ficção científica", "a science fiction scene with spaceships and machines"},
		{"horror", "terror", "a dark horror scene, unsettling and grim"},
		{"landscapes", "paisagens", "a wide landscape of hills, sea or sky"},
		{"cities and streets", "cidades e ruas", "a city street with buildings and crowds"},
		{"rooms and interiors", "quartos e interiores", "the inside of a room with furniture"},
		{"animals", "animais", "an animal"},
		{"plants and flowers", "plantas e flores", "plants, leaves and flowers"},
		{"clothes and outfits", "roupas e looks", "clothing and outfit details"},
		{"hands and anatomy", "mãos e anatomia", "studies of hands, faces and human anatomy"},
		{"machines and cars", "máquinas e carros", "cars, engines and machinery"},
		{"food", "comida", "food on a plate"},
		{"cute and pastel", "fofo e pastel", "cute pastel coloured art, soft and sweet"},
	}},
}

// themePrompts lists every phrase the theme line needs a text vector for. The
// browser encodes the missing ones once and they are cached with the search
// queries from then on.
func themePrompts() []string {
	prompts := []string{}
	for _, facet := range themeFacets {
		for _, option := range facet.Options {
			prompts = append(prompts, option.Prompt)
		}
	}
	return prompts
}

// missingThemePrompts is what the browser still has to encode. An empty list
// means every theme can be worked out on the server alone.
func (a *application) missingThemePrompts() []string {
	missing := []string{}
	for _, prompt := range themePrompts() {
		if _, found := a.queryEmbedding(prompt); !found {
			missing = append(missing, prompt)
		}
	}
	return missing
}

// themeCentroid averages the picture vectors of the given paths and returns
// it as a unit vector, along with how many pictures it was built from.
func (a *application) themeCentroid(paths []string) ([]float64, int) {
	dimensions := a.embeddingModel.Dimensions
	centroid := make([]float64, dimensions)
	counted := 0
	a.mu.RLock()
	for _, path := range paths {
		record, found := a.embeddings[path]
		if !found || len(record.Vector) != dimensions {
			continue
		}
		for index, value := range record.Vector {
			centroid[index] += float64(value)
		}
		counted++
	}
	a.mu.RUnlock()
	if counted == 0 {
		return nil, 0
	}
	length := 0.0
	for _, value := range centroid {
		length += value * value
	}
	length = math.Sqrt(length)
	if length == 0 {
		return nil, 0
	}
	for index := range centroid {
		centroid[index] /= length
	}
	return centroid, counted
}

// themeMinimumPictures is where a description starts being about the month
// rather than about the two pictures that happen to be in it.
const themeMinimumPictures = 4

// describeTheme names what a set of pictures is mostly about.
//
// Raw similarity to a phrase cannot be compared across phrases: CLIP holds
// some wordings closer to everything than others, so the same phrase would win
// every month in every library. What is subtracted is the same phrase's
// similarity to the library as a whole, which leaves how much this month leans
// that way compared to the rest of the collection. A month that really is the
// whole library still gets a name, because the comparison is a weighted one
// rather than a difference of equals.
func (a *application) describeTheme(paths []string, library []float64) string {
	centroid, counted := a.themeCentroid(paths)
	if centroid == nil || counted < themeMinimumPictures {
		return ""
	}
	language := a.language()
	words := []string{}
	for _, facet := range themeFacets {
		best, bestScore := "", math.Inf(-1)
		for _, option := range facet.Options {
			vector, found := a.queryEmbedding(option.Prompt)
			if !found {
				continue
			}
			score := themeSimilarity(vector, centroid) - 0.6*themeSimilarity(vector, library)
			if score > bestScore {
				best, bestScore = option.label(language), score
			}
		}
		if best != "" {
			words = append(words, best)
		}
	}
	if len(words) == 0 {
		return ""
	}
	return capitalizeFirst(strings.Join(words, ", "))
}

func themeSimilarity(vector []float32, centroid []float64) float64 {
	if len(centroid) != len(vector) {
		return 0
	}
	score := 0.0
	for index, value := range vector {
		score += float64(value) * centroid[index]
	}
	return score
}

func capitalizeFirst(value string) string {
	for index, letter := range value {
		return string(unicode.ToUpper(letter)) + value[index+len(string(letter)):]
	}
	return value
}
