package hostmsg

import (
	"strconv"
	"strings"
	"unicode"
)

// The detector reads the words of a message and scores each language by the
// function words it finds. It needs no model and no dependency, and it is
// built to be wrong in the safe direction: a short or doubtful message gives
// no answer, and the caller keeps the language it had.

// stopWords holds, for each language, space separated words. A word may carry
// a weight after "=": 2 for a word that belongs to the language alone, the
// default 1 for a common word, a smaller value for a word that other
// languages share.
var stopWords = map[string]string{
	English: "the=2 you=2 your=2 what=2 please=2 would=2 with=1.5 this=2 that=1.5 have=1.5 from=1.5 there=1.5 thanks=2 thank=2 hello=2 " +
		"could=2 how=1.5 where=1.5 why=1.5 are=1.5 is=1.5 and=1 to=0.5 of=1 in=0.5 it=1 for=1 can=1 not=1 do=1 my=1 but=1 will=1 " +
		"an=0.5 be=1 if=0.5 so=0.5 just=1.5 need=1.5 want=1.5 was=1 does=1.5 did=1.5 which=1.5 about=1.5 when=1.5 they=1.5 been=1.5 " +
		"i=0.5 im=1.5 dont=2 cant=2 hi=1 yes=0.5 some=1.5 any=1.5 me=0.3 send=1 file=1 morning=1 good=1",
	Italian: "che=1.5 non=1.5 è=1.5 sono=2 per=1 una=1 della=2 di=0.5 il=1 lo=0.5 gli=2 le=0.5 la=0.5 ciao=2 grazie=2 come=1 cosa=2 " +
		"quando=1 perché=2 perche=1.5 anche=2 questo=2 questa=2 sì=1.5 favore=2 puoi=2 posso=2 vorrei=2 mi=0.8 ti=1 ho=2 hai=2 più=2 " +
		"molto=2 bene=1.5 buongiorno=2 buonasera=2 dove=2 quanto=1.5 sto=1.5 settimana=2 scorsa=2 riassunto=2 stai=2 nel=2 nella=2 sul=1.5 con=0.5 un=0.5 uno=1 ma=1 " +
		"già=2 ancora=1.5 adesso=2 ora=1 invia=2 mandami=2 dammi=2 fammi=2 scrivi=2 dimmi=2 dei=1 delle=2 degli=2 alla=2 allo=2 dal=1.5 " +
		"dalla=2 tutto=1.5 tutti=1.5 niente=2 nulla=2 aiutarmi=2 aiuto=1.5 serve=1.5 vuoi=2 voglio=2 devo=2 puoi=2 cosa=2 sei=1.5 " +
		"loro=1 nostro=1.5 mio=1.5 mia=1.5 tuo=1.5 file=0.3 poi=1.5 oggi=2 domani=2 ieri=2 salve=2 certo=1.5 magari=2",
	German: "der=1.5 die=1.5 das=1.5 und=2 ist=2 nicht=2 ich=2 du=2 ein=1.5 eine=2 zu=1 mit=1.5 für=2 auf=1 bitte=2 danke=2 hallo=2 " +
		"wie=1 was=1 wer=1.5 wo=1 warum=2 kannst=2 kann=1.5 mir=1.5 mich=2 dir=1.5 dich=2 sind=1.5 bin=1.5 habe=2 hast=2 haben=1.5 " +
		"auch=1.5 noch=1 schon=1.5 nur=1 aber=1.5 oder=1.5 wenn=1.5 dass=2 dem=1.5 den=1 des=1 im=1 vom=1.5 zum=1.5 zur=1.5 einen=2 " +
		"einer=2 über=2 nach=1 bei=1 von=1 aus=1 nein=2 gut=1 guten=2 morgen=1 jetzt=2 heute=2 mein=1.5 meine=2 dein=1.5 deine=2 " +
		"brauche=2 möchte=2 will=1 soll=1.5 sag=1.5 schick=2 schicke=2 sende=1.5 datei=2 bitte=2 genau=1.5 klar=1.5 vielen=2 wieder=2 woche=2 letzte=2 letzten=2",
	Spanish: "el=1.5 la=0.5 los=2 las=1.5 de=0.5 que=1 en=0.5 es=1 un=0.5 una=0.5 por=1 para=1.5 con=0.5 se=0.5 mi=0.5 hola=2 gracias=2 " +
		"favor=1.5 puedes=2 puedo=2 quiero=2 qué=2 cómo=2 cuándo=2 dónde=2 está=1.5 estoy=2 estás=2 muy=1.5 también=2 pero=1.5 " +
		"porque=1.5 este=1 esta=1 ese=1 esto=1.5 eso=1.5 todo=1 nada=1.5 ahora=2 aquí=2 buenos=2 buenas=1.5 días=1.5 envía=2 " +
		"mándame=2 dime=2 dame=2 hay=2 sí=1.5 ya=1.5 más=1.5 sobre=1 del=1.5 al=1 tengo=2 tienes=2 necesito=2 vale=1 archivo=2 " +
		"semana=1.5 pasada=2 enviarme=2 ayudarme=2 puede=1.5 quieres=2 entonces=2 después=2 hoy=2 mañana=1.5 ayer=2 mis=1.5 tus=1.5 nuestro=1.5 favor=1.5",
	French: "le=1 la=0.5 les=2 des=1.5 du=1.5 de=0.5 un=0.5 une=1 et=1 est=1.5 que=1 qui=1 pour=1.5 avec=1.5 dans=1.5 sur=1 ne=0.5 pas=1.5 " +
		"je=2 tu=1.5 il=0.5 vous=2 nous=1.5 bonjour=2 merci=2 salut=1.5 plaît=2 peux=2 peut=1 veux=2 voudrais=2 ça=2 très=2 aussi=1.5 " +
		"mais=1.5 où=1.5 quand=1 comment=1.5 pourquoi=2 quoi=2 oui=1.5 ici=1.5 maintenant=2 envoie=2 envoyer=1.5 fichier=2 mon=1 ma=0.5 " +
		"mes=2 ton=1 ce=1 cette=2 ces=2 être=1.5 avoir=1.5 ai=1.5 suis=2 fait=1.5 tout=1 rien=2 j=0 besoin=2 aide=1.5 demain=2 " +
		"semaine=2 dernière=2 aujourd=2 hier=2 donne=1.5 s=0 m=0 t=0 c=0 d=0 l=0 qu=1 mercredi=1 bien=0.5",
	Portuguese: "o=0.5 os=2 as=1 de=0.5 do=1.5 da=1.5 dos=2 das=2 em=1.5 um=1 uma=0.5 que=1 não=2 nao=1 é=1 para=1.5 por=0.5 com=0.5 " +
		"você=2 vocês=2 olá=2 oi=1.5 obrigado=2 obrigada=2 pode=2 posso=2 quero=2 preciso=2 tenho=2 está=1.5 estou=2 muito=1 também=2 " +
		"mas=1 porque=1 isso=2 isto=2 esse=1 essa=1.5 ele=1 ela=1 nós=1 eu=1.5 meu=1.5 minha=2 aqui=1.5 agora=2 bom=1.5 boa=1.5 " +
		"tarde=1 manda=1.5 sim=2 já=1.5 mais=1 ainda=2 tudo=1.5 nada=0.5 arquivo=2 enviar=1.5 ajudar=1.5 então=2 depois=1.5 hoje=2 " +
		"semana=1.5 passada=2 gastei=2 resumo=1.5 quanto=1.5 amanhã=2 ontem=2 minhas=2 seus=1.5 sua=1.5 quer=1.5 fazer=1.5 dia=0.5 bom=1.5",
}

// letterHints are characters that point to a language.
var letterHints = map[rune]map[string]float64{
	'ß': {German: 3},
	'ñ': {Spanish: 3},
	'¿': {Spanish: 3},
	'¡': {Spanish: 3},
	'ã': {Portuguese: 3},
	'õ': {Portuguese: 3},
	'œ': {French: 2},
	'ì': {Italian: 1.5},
	'ò': {Italian: 1.5},
}

var weights = buildWeights()

func buildWeights() map[string]map[string]float64 {
	w := map[string]map[string]float64{}
	for lang, list := range stopWords {
		for _, f := range strings.Fields(list) {
			word, val := f, 1.0
			if i := strings.IndexByte(f, '='); i >= 0 {
				word = f[:i]
				if x, err := strconv.ParseFloat(f[i+1:], 64); err == nil {
					val = x
				}
			}
			if val <= 0 {
				continue
			}
			if w[word] == nil {
				w[word] = map[string]float64{}
			}
			w[word][lang] = val
		}
	}
	return w
}

// Scores of a language beyond which the detector speaks.
const (
	// MinScore is the score that a language needs to be named at all: two
	// ordinary function words, or one that belongs to the language alone.
	MinScore = 2.0
	// MinMargin is how far the best language must be ahead of the second.
	MinMargin = 1.0
	// SwitchScore is the score that a message needs to move a conversation
	// from the language it had to another one. A short reply ("ok", "thanks")
	// never reaches it; a sentence does.
	SwitchScore = 4.0
)

// Detection is the result of reading a message.
type Detection struct {
	// Lang is the language, or "" when the message does not say clearly.
	Lang string
	// Score is the score of Lang (or of the best candidate when Lang is empty).
	Score float64
}

// Detect reads the language of a message among the languages of the catalog.
func Detect(text string) Detection {
	scores := map[string]float64{}
	var word []rune
	flush := func() {
		if len(word) == 0 {
			return
		}
		for lang, v := range weights[string(word)] {
			scores[lang] += v
		}
		word = word[:0]
	}
	for _, r := range strings.ToLower(text) {
		if h, ok := letterHints[r]; ok {
			for lang, v := range h {
				scores[lang] += v
			}
		}
		if unicode.IsLetter(r) {
			word = append(word, r)
			continue
		}
		flush()
	}
	flush()
	best, second, bestLang := 0.0, 0.0, ""
	for _, lang := range Languages {
		s := scores[lang]
		switch {
		case s > best:
			second, best, bestLang = best, s, lang
		case s > second:
			second = s
		}
	}
	if best < MinScore || best-second < MinMargin {
		return Detection{Score: best}
	}
	return Detection{Lang: bestLang, Score: best}
}
