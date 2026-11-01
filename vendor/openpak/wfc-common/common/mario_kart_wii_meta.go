package common

import (
	"encoding/binary"
	"errors"
	"fmt"
	"unicode/utf16"
)

// RKGMetadata is what the header of a Mario Kart Wii RKG ghost file says about the run. The
// layout is the one documented by the community (Wiimm's RKG notes, wiki.tockdom.com "RKG",
// and the WiiLink docs): big-endian bit fields in the first 0x88 bytes, the Mii data at 0x3C.
// Only facts from those documents are used; unknown header bytes are not interpreted.
type RKGMetadata struct {
	FinishTimeMS    int
	LapTimesMS      []int // one per lap, as the header stores them
	Course          MarioKartWiiCourseId
	Vehicle         MarioKartWiiVehicleId
	Character       MarioKartWiiCharacterId
	Controller      MarioKartWiiControllerId
	Year            int // full year (header stores years since 2000)
	Month           int
	Day             int
	Compressed      bool
	GhostType       int
	DriftType       int // 0 = manual, 1 = automatic
	InputDataLength int
	LapCount        int
	CountryCode     byte
	StateCode       byte
	LocationCode    uint16
	MiiName         string
}

// ErrRKGTooShort is a file smaller than the RKG header.
var ErrRKGTooShort = errors.New("rkg: file shorter than the 0x8C byte header")

// ErrRKGMagic is a file that does not start with "RKGD".
var ErrRKGMagic = errors.New("rkg: bad magic")

// rkgHeaderSize is the fixed header before the (optionally compressed) input data.
const rkgHeaderSize = 0x8C

// ParseRKGMetadata reads the header of an RKG file. It validates only what it needs to read
// (magic and length); it does not judge whether the ghost is legitimate.
func ParseRKGMetadata(file []byte) (RKGMetadata, error) {
	var m RKGMetadata
	if len(file) < rkgHeaderSize {
		return m, ErrRKGTooShort
	}
	if string(file[:4]) != "RKGD" {
		return m, ErrRKGMagic
	}
	g := RKGhostData(file)
	m.FinishTimeMS = g.GetTime(0)
	m.LapCount = g.GetLapCount()
	laps := m.LapCount
	if laps > 5 {
		laps = 5 // the header has room for five lap times
	}
	for lap := 1; lap <= laps; lap++ {
		m.LapTimesMS = append(m.LapTimesMS, g.GetTime(lap))
	}
	m.Course = g.GetCourse()
	m.Vehicle = g.GetVehicle()
	m.Character = g.GetCharacter()
	m.Controller = g.GetController()
	m.Year = 2000 + g.GetYear()
	m.Month = g.GetMonth()
	m.Day = g.GetDay()
	m.Compressed = g.IsCompressed()
	m.GhostType = g.GetGhostType()
	m.DriftType = g.GetDriftType()
	m.InputDataLength = int(g.GetInputDataLength())
	m.CountryCode = g.GetCountryCode()
	m.StateCode = g.GetStateCode()
	m.LocationCode = g.GetLocationCode()
	m.MiiName = MiiNameFromData(file[0x3C : 0x3C+0x4C])
	return m, nil
}

// MiiNameFromData is the Mii name in a Mii data block (or a player-info blob, which starts with
// one): ten UTF-16 big-endian characters at offset 2, ended by a null.
func MiiNameFromData(mii []byte) string {
	if len(mii) < 0x16 {
		return ""
	}
	units := make([]uint16, 0, 10)
	for i := 0; i < 10; i++ {
		u := binary.BigEndian.Uint16(mii[2+i*2:])
		if u == 0 {
			break
		}
		units = append(units, u)
	}
	return string(utf16.Decode(units))
}

// FormatMKWTime renders a time in milliseconds the way the game shows it, m:ss.mmm.
func FormatMKWTime(ms int) string {
	if ms < 0 {
		ms = 0
	}
	return fmt.Sprintf("%d:%02d.%03d", ms/60000, ms/1000%60, ms%1000)
}

var mkwCourseNames = [...]string{
	"Mario Circuit", "Moo Moo Meadows", "Mushroom Gorge", "Grumble Volcano", "Toad's Factory",
	"Coconut Mall", "DK Summit", "Wario's Gold Mine", "Luigi Circuit", "Daisy Circuit",
	"Moonview Highway", "Maple Treeway", "Bowser's Castle", "Rainbow Road", "Dry Dry Ruins",
	"Koopa Cape", "GCN Peach Beach", "GCN Mario Circuit", "GCN Waluigi Stadium", "GCN DK Mountain",
	"DS Yoshi Falls", "DS Desert Hills", "DS Peach Gardens", "DS Delfino Square",
	"SNES Mario Circuit 3", "SNES Ghost Valley 2", "N64 Mario Raceway", "N64 Sherbet Land",
	"N64 Bowser's Castle", "N64 DK's Jungle Parkway", "GBA Bowser Castle 3", "GBA Shy Guy Beach",
}

var mkwVehicleNames = [...]string{
	"Standard Kart S", "Standard Kart M", "Standard Kart L", "Booster Seat", "Classic Dragster",
	"Offroader", "Mini Beast", "Wild Wing", "Flame Flyer", "Cheep Charger", "Super Blooper",
	"Piranha Prowler", "Tiny Titan", "Daytripper", "Jetsetter", "Blue Falcon", "Sprinter",
	"Honeycoupe", "Standard Bike S", "Standard Bike M", "Standard Bike L", "Bullet Bike",
	"Mach Bike", "Flame Runner", "Bit Bike", "Sugarscoot", "Wario Bike", "Quacker", "Zip Zip",
	"Shooting Star", "Magikruiser", "Sneakster", "Spear", "Jet Bubble", "Dolphin Dasher", "Phantom",
}

var mkwCharacterNames = [...]string{
	"Mario", "Baby Peach", "Waluigi", "Bowser", "Baby Daisy", "Dry Bones", "Baby Mario", "Luigi",
	"Toad", "Donkey Kong", "Yoshi", "Wario", "Baby Luigi", "Toadette", "Koopa Troopa", "Daisy",
	"Peach", "Birdo", "Diddy Kong", "King Boo", "Bowser Jr.", "Dry Bowser", "Funky Kong", "Rosalina",
	"Small Mii A (male)", "Small Mii A (female)", "Small Mii B (male)", "Small Mii B (female)",
	"Small Mii C (male)", "Small Mii C (female)", "Medium Mii A (male)", "Medium Mii A (female)",
	"Medium Mii B (male)", "Medium Mii B (female)", "Medium Mii C (male)", "Medium Mii C (female)",
	"Large Mii A (male)", "Large Mii A (female)", "Large Mii B (male)", "Large Mii B (female)",
	"Large Mii C (male)", "Large Mii C (female)",
}

var mkwControllerNames = [...]string{"Wii Wheel", "Wii Remote + Nunchuk", "Classic Controller", "GameCube Controller"}

func nameAt(names []string, i int) string {
	if i < 0 || i >= len(names) {
		return ""
	}
	return names[i]
}

func (id MarioKartWiiCourseId) Name() string     { return nameAt(mkwCourseNames[:], int(id)) }
func (id MarioKartWiiVehicleId) Name() string    { return nameAt(mkwVehicleNames[:], int(id)) }
func (id MarioKartWiiCharacterId) Name() string  { return nameAt(mkwCharacterNames[:], int(id)) }
func (id MarioKartWiiControllerId) Name() string { return nameAt(mkwControllerNames[:], int(id)) }
