package bench

import (
	"context"
	"fmt"

	"github.com/AlexS8332/AnimalGuide/internal/agents"
	"github.com/AlexS8332/AnimalGuide/internal/card"
	"github.com/AlexS8332/AnimalGuide/internal/features"
)

// Facts — И-1, достоверность: карточка только из прочитанного в этом
// прогоне, латынь только подтверждённая GBIF, выдумки и «похожее» —
// отвергнуты.
//
// Контрольная дорожка — тот же агент без завершающих инструментов и
// трекера, с теми же правилами словами в промпте.
type Facts struct {
	// Real — настоящие названия, включая редкие; Fake — выдумки; Hard —
	// похожие на настоящие, но несуществующие.
	Real, Fake, Hard []string
	// Topic — раздел, который читается кликом у каждой карточки.
	Topic string
}

// NewFacts — сценарий ТЗ: 8 настоящих, 4 выдумки, 3 трудных случая.
func NewFacts() *Facts {
	return &Facts{
		Real:  []string{"рысь", "манул", "неясыть", "поручейник", "росомаха", "барсук", "выхухоль", "серый журавль"},
		Fake:  []string{"шурундук пятнистый", "полосатый камнегрыз", "северный пыжехвост", "болотная хвостокрылка"},
		Hard:  []string{"малая выхухоль", "полосатый манул", "карликовая росомаха"},
		Topic: "diet",
	}
}

func (*Facts) ID() string    { return "И-1" }
func (*Facts) Title() string { return "Достоверность" }

// factsLanes — основная и контрольная дорожки И-1.
func factsLanes(base features.Set) []Lane {
	return []Lane{
		{Name: "основная", Note: "завершающие инструменты с проверкой по трекеру", Features: base},
		{Name: "без трекера", Note: "тот же агент, карточка принимается текстом; те же правила словами в промпте",
			Features: base.With(features.Tracker, false)},
	}
}

// laneTally — счёт одной дорожки И-1.
type laneTally struct {
	real, fake, hard         int
	unconfirmed, unread      int
	unconfirmedEx, unreadEx  []string
	missedReal, acceptedFake []string
}

func (f *Facts) Run(ctx context.Context, s *Stand, r *Result) error {
	r.Goal = "факт — только из прочитанного источника, латынь — только подтверждённая GBIF, выдумки и похожее отвергнуты"
	lanes := factsLanes(s.env.Base)
	r.describeLanes(s.env.Registry, lanes)
	tally := map[string]*laneTally{}
	for _, l := range lanes {
		tally[l.Name] = &laneTally{}
	}
	type query struct {
		name, kind string
	}
	var queries []query
	for _, q := range f.Real {
		queries = append(queries, query{q, "real"})
	}
	for _, q := range f.Fake {
		queries = append(queries, query{q, "fake"})
	}
	for _, q := range f.Hard {
		queries = append(queries, query{q, "hard"})
	}
	for _, q := range queries {
		g, err := s.Group("И-1: "+q.name, lanes)
		if err != nil {
			return err
		}
		r.Mechanism = g.Mechanism
		steps, err := g.Send(ctx, agents.Request{Kind: agents.KindOpen, Name: q.name, Text: q.name})
		if err != nil {
			return err
		}
		for i, st := range steps {
			t := tally[st.Lane]
			cards := deltas(st.Turn, card.DeltaCard)
			found := len(cards) > 0 && cards[0].Card != nil
			switch q.kind {
			case "real":
				if found {
					t.real++
				} else {
					t.missedReal = append(t.missedReal, q.name)
				}
			case "fake", "hard":
				if !found && len(deltas(st.Turn, card.DeltaNotFound)) > 0 {
					if q.kind == "fake" {
						t.fake++
					} else {
						t.hard++
					}
				} else {
					t.acceptedFake = append(t.acceptedFake, q.name)
				}
			}
			if !found {
				if q.kind != "real" {
					r.sample("отвергнуто: "+q.name, st, "")
				}
				continue
			}
			c := *cards[0].Card
			if c.Latin != "" && (c.Unverified || !latinConfirmed(st.Turn, c.Latin)) {
				t.unconfirmed++
				t.unconfirmedEx = append(t.unconfirmedEx, fmt.Sprintf("%s → %s", q.name, c.Latin))
			}
			if q.kind != "real" {
				r.sample("принято вместо отказа: "+q.name, st, c.Title())
			}
			if f.Topic == "" {
				continue
			}
			sec, err := g.Dialogs[i].Send(ctx, agents.Request{Kind: agents.KindSection, CardID: c.ID, Topic: f.Topic})
			if err != nil {
				return err
			}
			for _, d := range deltas(sec.Turn, card.DeltaSection) {
				if d.Section == nil || d.Section.Status != card.SectionRead {
					continue
				}
				if !sectionRead(sec.Turn, *d.Section) {
					t.unread++
					t.unreadEx = append(t.unreadEx, fmt.Sprintf("%s: %s", c.Name, d.Section.Title))
				}
			}
		}
	}
	main := lanes[0].Name
	for _, l := range lanes {
		t := tally[l.Name]
		if l.Name == main {
			r.atLeast("настоящие опознаны", main, t.real, len(f.Real), len(f.Real))
			r.atLeast("выдумки отвергнуты", main, t.fake, len(f.Fake), len(f.Fake))
			r.atLeast("трудные случаи отвергнуты", main, t.hard, len(f.Hard), len(f.Hard))
			r.zero("карточек с латынью без подтверждения GBIF", main, t.unconfirmed, t.unconfirmedEx)
			r.zero("разделов без прочитанного источника", main, t.unread, t.unreadEx)
		}
		r.metric("настоящие опознаны", l.Name, "%d из %d", t.real, len(f.Real))
		r.metric("выдумки и трудные отвергнуты", l.Name, "%d из %d", t.fake+t.hard, len(f.Fake)+len(f.Hard))
		r.metric("латынь без подтверждения GBIF", l.Name, "%d", t.unconfirmed)
		r.metric("разделов без прочитанного источника", l.Name, "%d", t.unread)
		if len(t.missedReal) > 0 {
			r.note("%s: не опознаны %v", l.Name, t.missedReal)
		}
		if len(t.acceptedFake) > 0 {
			r.note("%s: карточка вместо отказа — %v", l.Name, t.acceptedFake)
		}
	}
	return nil
}
