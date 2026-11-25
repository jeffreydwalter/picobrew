package server

import (
	"errors"
	"reflect"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/recipe"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/store"
)

func (s *Server) stockGroupProgram(i brewInvite) (stockItem, error) {
	var src groupRecipe
	var item stockItem
	if i.GroupKitSelection.Present() {
		return item, errors.New(kitGroupRequirement)
	}
	if i.Action != "brew" {
		return item, errors.New("Only saved recipes can be sent to a Zymatic menu")
	}
	if _, err := s.db.Get("group-brew-program", i.GroupSessionID, &src); err != nil {
		return item, err
	}
	saved, err := s.groupLibrary(src)
	if err != nil {
		return item, err
	}
	profile, ok := s.selectProfile("zymatic")
	if !ok {
		return item, errors.New("The Zymatic equipment profile is unavailable")
	}
	return compileStockRecipe(saved, profile)
}
func (s *Server) prepareMenuInvite(i *brewInvite, serial string) error {
	i.MenuSerial = serial
	if _, err := s.stockGroupProgram(*i); err != nil {
		i.State = "unavailable"
		i.Message = err.Error()
	} else {
		i.State = "pending"
		i.Message = "Accept to add this recipe to the Zymatic menu. Sync recipes and start it at the dial."
	}
	return s.saveInvite(*i)
}

// Menu delivery is owner-confirmed, scoped to one serial and additive. It
// does not replace an existing revision or synthesize a Zymatic start.
func (s *Server) acceptGroupMenu(i *brewInvite) error {
	item, err := s.stockGroupProgram(*i)
	if err != nil {
		return err
	}
	s.stockMu.Lock()
	defer s.stockMu.Unlock()
	var b stockBrewer
	rev, err := s.db.Get(stockKind, i.MenuSerial, &b)
	if errors.Is(err, store.ErrNotFound) {
		rev = 0
		b.Machine = i.MenuSerial
	} else if err != nil {
		return err
	}
	if b.Session.ID != "" && !b.Session.Ended {
		return errors.New("The Zymatic has an active session")
	}
	count := 0
	found := false
	for _, entry := range b.Items {
		if entry.RecipeID == item.RecipeID || entry.Program.ID == item.Program.ID {
			if !reflect.DeepEqual(item, entry) {
				return errors.New("The Zymatic menu already contains a different version of this recipe")
			}
			found = true
		}
		if entry.Maintenance == item.Maintenance && recipe.ValidateStockZymaticProgram(entry.Program) == nil {
			count++
		}
	}
	if !found {
		if count >= 10 {
			return errors.New("The Zymatic menu is full")
		}
		b.Items = append(b.Items, item)
		if b.User == "" {
			b.User = randomID()
		}
		if _, err = s.db.Put(stockKind, b.Machine, b, rev); err != nil {
			return err
		}
	}
	i.State = "accepted"
	i.Message = "Recipe available on the Zymatic. Sync its menu and start at the physical dial."
	return s.saveInvite(*i)
}
