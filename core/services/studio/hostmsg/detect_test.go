package hostmsg_test

import (
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/hostmsg"
)

// detectTable holds real short messages, as people write them to a bot.
var detectTable = []struct{ text, want string }{
	{"Can you send me the report from yesterday?", "en"},
	{"Hello, what is the weather like in Rome?", "en"},
	{"please remind me to call the dentist at 5", "en"},
	{"I need a summary of this file", "en"},
	{"Thanks, that is what I wanted", "en"},
	{"Why did the job fail again?", "en"},
	{"Ciao, puoi mandarmi il file di ieri?", "it"},
	{"Come sto andando con le spese di questo mese?", "it"},
	{"Grazie mille, è perfetto", "it"},
	{"Buongiorno, vorrei sapere quanto costa", "it"},
	{"Ricordami di chiamare il dentista domani alle 5", "it"},
	{"Non ho capito cosa devo fare", "it"},
	{"Dove trovo i documenti che ti ho dato?", "it"},
	{"Hallo, kannst du mir die Datei von gestern schicken?", "de"},
	{"Danke, das ist genau was ich brauche", "de"},
	{"Ich habe das nicht verstanden, bitte noch einmal", "de"},
	{"Wie spät ist es jetzt in Berlin?", "de"},
	{"Erinnere mich morgen an den Termin beim Zahnarzt", "de"},
	{"Hola, puedes enviarme el archivo de ayer?", "es"},
	{"Gracias, eso es lo que necesito", "es"},
	{"¿Cuánto cuesta esto?", "es"},
	{"No entiendo qué tengo que hacer ahora", "es"},
	{"Recuérdame llamar al dentista mañana por la tarde", "es"},
	{"Bonjour, tu peux m'envoyer le fichier d'hier ?", "fr"},
	{"Merci beaucoup, c'est exactement ce qu'il me faut", "fr"},
	{"Je n'ai pas compris, pouvez-vous répéter s'il vous plaît", "fr"},
	{"Où sont les documents que je vous ai donnés ?", "fr"},
	{"Rappelle-moi d'appeler le dentiste demain", "fr"},
	{"Can you remind me tomorrow morning about the meeting?", "en"},
	{"What did I spend on groceries last week?", "en"},
	{"Quanto ho speso per la spesa la settimana scorsa?", "it"},
	{"Mi servirebbe un riassunto di questo documento, grazie", "it"},
	{"Sì, va bene, procedi pure", "it"},
	{"Wie viel habe ich letzte Woche für Lebensmittel ausgegeben?", "de"},
	{"Ja, das ist in Ordnung, bitte weitermachen", "de"},
	{"¿Cuánto gasté en comida la semana pasada?", "es"},
	{"Necesito un resumen de este documento, por favor", "es"},
	{"Combien ai-je dépensé en courses la semaine dernière ?", "fr"},
	{"J'ai besoin d'un résumé de ce document, merci", "fr"},
	{"Quanto gastei com mercado na semana passada?", "pt-BR"},
	{"Preciso de um resumo deste documento, por favor", "pt-BR"},
	{"Olá, você pode me enviar o arquivo de ontem?", "pt-BR"},
	{"Obrigado, é isso mesmo que eu preciso", "pt-BR"},
	{"Não entendi o que tenho que fazer agora", "pt-BR"},
	{"Quanto custa isso? Eu não tenho certeza", "pt-BR"},
	{"Me lembre de ligar para o dentista amanhã", "pt-BR"},
}

// quiet holds messages that say nothing about the language: the detector must
// stay silent, so that the language of the conversation does not flip.
var quiet = []string{"ok", "OK", "sì", "si", "yes", "no", "👍", "7K2Q", "ok grazie", "thanks", "danke", "merci", "gracias", "obrigado", "vale", "", "12:30", "https://example.com/a", "..."}

func TestDetectTable(t *testing.T) {
	ok := 0
	for _, c := range detectTable {
		got := hostmsg.Detect(c.text)
		if got.Lang == c.want {
			ok++
			continue
		}
		t.Errorf("Detect(%q) = %q (%.1f), want %q", c.text, got.Lang, got.Score, c.want)
	}
	t.Logf("accuracy: %d/%d", ok, len(detectTable))
}

func TestDetectStaysQuietOnOneWord(t *testing.T) {
	for _, s := range quiet {
		if got := hostmsg.Detect(s); got.Lang != "" && got.Score >= hostmsg.SwitchScore {
			t.Errorf("Detect(%q) = %q (%.1f): a one word message must not be able to switch the language", s, got.Lang, got.Score)
		}
	}
	for _, s := range []string{"ok", "OK", "si", "yes", "no", "👍", "7K2Q", "vale"} {
		if got := hostmsg.Detect(s); got.Lang != "" {
			t.Errorf("Detect(%q) = %q: expected no answer", s, got.Lang)
		}
	}
}

func TestDetectAllLanguagesAreCovered(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range detectTable {
		seen[c.want] = true
	}
	for _, l := range hostmsg.Languages {
		if !seen[l] {
			t.Errorf("no detection example for %s", l)
		}
	}
}
