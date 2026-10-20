package match

import (
	"math"
	"sort"
	"sync"
	"time"

	"github.com/abjelosevic88/pocket-homelab-documents/internal/store"
)

// Classifier is a multinomial naive Bayes model over a person's filed
// documents: it suggests a correspondent, a document type, a storage path
// and tags for a new one. It is small enough to retrain in memory whenever
// something changed, so there is no model file to manage.
type Classifier struct {
	trainedAt time.Time
	docs      int
	corr      *multiclass
	typ       *multiclass
	path      *multiclass
	tags      map[int64]*binary
	vocab     map[string]bool
}

// MinDocs is how many filed examples a label needs before it is suggested.
const MinDocs = 2

type multiclass struct {
	classDocs  map[int64]int
	classWords map[int64]map[string]int
	classTotal map[int64]int
	total      int
}

type binary struct {
	pos, neg *multiclass // pos has class 1, neg has class 0
}

func newMulti() *multiclass {
	return &multiclass{classDocs: map[int64]int{}, classWords: map[int64]map[string]int{}, classTotal: map[int64]int{}}
}

func (m *multiclass) add(class int64, words map[string]int) {
	m.classDocs[class]++
	m.total++
	if m.classWords[class] == nil {
		m.classWords[class] = map[string]int{}
	}
	for w, n := range words {
		m.classWords[class][w] += n
		m.classTotal[class] += n
	}
}

// scores returns log P(class | words) per class, normalised to probabilities.
func (m *multiclass) scores(words map[string]int, vocab int) map[int64]float64 {
	if m.total == 0 {
		return nil
	}
	logp := map[int64]float64{}
	for class, nd := range m.classDocs {
		lp := math.Log(float64(nd) / float64(m.total))
		cw := m.classWords[class]
		denom := float64(m.classTotal[class] + vocab)
		for w, n := range words {
			lp += float64(n) * math.Log((float64(cw[w])+1)/denom)
		}
		logp[class] = lp
	}
	// softmax
	maxv := math.Inf(-1)
	for _, v := range logp {
		if v > maxv {
			maxv = v
		}
	}
	sum := 0.0
	for _, v := range logp {
		sum += math.Exp(v - maxv)
	}
	out := map[int64]float64{}
	for c, v := range logp {
		out[c] = math.Exp(v-maxv) / sum
	}
	return out
}

func bag(text string) map[string]int {
	out := map[string]int{}
	toks := store.Tokens(text)
	if len(toks) > 3000 {
		toks = toks[:3000]
	}
	for _, t := range toks {
		out[t]++
	}
	return out
}

// Train builds a model from a person's documents.
func Train(docs []store.TrainingDoc) *Classifier {
	c := &Classifier{trainedAt: time.Now(), docs: len(docs), corr: newMulti(), typ: newMulti(), path: newMulti(), tags: map[int64]*binary{}, vocab: map[string]bool{}}
	bags := make([]map[string]int, len(docs))
	allTags := map[int64]bool{}
	for i, d := range docs {
		bags[i] = bag(d.Text)
		for w := range bags[i] {
			c.vocab[w] = true
		}
		for _, t := range d.Tags {
			allTags[t] = true
		}
	}
	for t := range allTags {
		c.tags[t] = &binary{pos: newMulti(), neg: newMulti()}
	}
	for i, d := range docs {
		if d.CorrespondentID > 0 {
			c.corr.add(d.CorrespondentID, bags[i])
		}
		if d.DocumentTypeID > 0 {
			c.typ.add(d.DocumentTypeID, bags[i])
		}
		if d.StoragePathID > 0 {
			c.path.add(d.StoragePathID, bags[i])
		}
		has := map[int64]bool{}
		for _, t := range d.Tags {
			has[t] = true
		}
		for t, b := range c.tags {
			if has[t] {
				b.pos.add(1, bags[i])
			} else {
				b.neg.add(0, bags[i])
			}
		}
	}
	return c
}

// Suggestion is what the classifier thinks of a document.
type Suggestion struct {
	Correspondent *Pick  `json:"correspondent"`
	DocumentType  *Pick  `json:"document_type"`
	StoragePath   *Pick  `json:"storage_path"`
	Tags          []Pick `json:"tags"`
	TrainedOn     int    `json:"trained_on"`
}

// Pick is one label with its confidence.
type Pick struct {
	ID         int64   `json:"id"`
	Confidence float64 `json:"confidence"`
}

// Suggest classifies a text. Threshold is the posterior a label needs.
func (c *Classifier) Suggest(text string, threshold float64) Suggestion {
	s := Suggestion{Tags: []Pick{}, TrainedOn: c.docs}
	if c == nil || c.docs == 0 {
		return s
	}
	words := bag(text)
	vocab := len(c.vocab) + 1
	best := func(m *multiclass) *Pick {
		sc := m.scores(words, vocab)
		var bp *Pick
		for class, p := range sc {
			if m.classDocs[class] < MinDocs {
				continue
			}
			if bp == nil || p > bp.Confidence {
				bp = &Pick{ID: class, Confidence: p}
			}
		}
		if bp == nil || bp.Confidence < threshold {
			return nil
		}
		// A single class always wins the softmax: ask for a margin of evidence instead.
		if len(m.classDocs) == 1 {
			if !c.leansToward(m, bp.ID, words, vocab) {
				return nil
			}
			bp.Confidence = 0.75
		}
		return bp
	}
	s.Correspondent = best(c.corr)
	s.DocumentType = best(c.typ)
	s.StoragePath = best(c.path)
	for t, b := range c.tags {
		if b.pos.classDocs[1] < MinDocs || b.neg.classDocs[0] == 0 {
			continue
		}
		both := newMulti()
		both.classDocs[1], both.classWords[1], both.classTotal[1] = b.pos.classDocs[1], b.pos.classWords[1], b.pos.classTotal[1]
		both.classDocs[0], both.classWords[0], both.classTotal[0] = b.neg.classDocs[0], b.neg.classWords[0], b.neg.classTotal[0]
		both.total = both.classDocs[0] + both.classDocs[1]
		if p := both.scores(words, vocab)[1]; p >= threshold {
			s.Tags = append(s.Tags, Pick{ID: t, Confidence: p})
		}
	}
	sort.Slice(s.Tags, func(i, j int) bool { return s.Tags[i].Confidence > s.Tags[j].Confidence })
	return s
}

// leansToward checks a lone class against "everything else" (the documents
// without the label), so one filed invoice does not make every document one.
func (c *Classifier) leansToward(m *multiclass, class int64, words map[string]int, vocab int) bool {
	rest := newMulti()
	rest.classDocs[0] = c.docs - m.classDocs[class]
	if rest.classDocs[0] <= 0 {
		return true
	}
	rest.total = rest.classDocs[0]
	rest.classWords[0] = map[string]int{}
	for w := range c.vocab {
		rest.classWords[0][w] = 1
	}
	rest.classTotal[0] = len(c.vocab)
	both := newMulti()
	both.classDocs[1], both.classWords[1], both.classTotal[1] = m.classDocs[class], m.classWords[class], m.classTotal[class]
	both.classDocs[0], both.classWords[0], both.classTotal[0] = rest.classDocs[0], rest.classWords[0], rest.classTotal[0]
	both.total = both.classDocs[0] + both.classDocs[1]
	return both.scores(words, vocab)[1] >= 0.7
}

// Cache keeps one classifier per person, retrained when the data changed.
type Cache struct {
	mu     sync.Mutex
	models map[int64]*Classifier
	dirty  map[int64]bool
}

// NewCache makes an empty cache.
func NewCache() *Cache { return &Cache{models: map[int64]*Classifier{}, dirty: map[int64]bool{}} }

// Invalidate marks a person's model stale.
func (c *Cache) Invalidate(userID int64) {
	c.mu.Lock()
	c.dirty[userID] = true
	c.mu.Unlock()
}

// Get returns a fresh enough model, training it through load when needed.
func (c *Cache) Get(userID int64, load func() ([]store.TrainingDoc, error)) (*Classifier, error) {
	c.mu.Lock()
	m, ok := c.models[userID]
	stale := c.dirty[userID] || !ok || time.Since(m.trainedAt) > 30*time.Minute
	c.mu.Unlock()
	if !stale {
		return m, nil
	}
	docs, err := load()
	if err != nil {
		return nil, err
	}
	m = Train(docs)
	c.mu.Lock()
	c.models[userID] = m
	c.dirty[userID] = false
	c.mu.Unlock()
	return m, nil
}
