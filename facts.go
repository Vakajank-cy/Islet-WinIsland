package main

import "time"

// Daily facts: short, written for this app. One per day, same for everyone.
var facts = [][3]string{
	{"Medūzos neturi nei smegenų, nei širdies, bet gyvena Žemėje jau daugiau nei 500 milijonų metų.",
		"Jellyfish have no brain and no heart, yet they have lived on Earth for over 500 million years.",
		"У медуз нет ни мозга, ни сердца, но они живут на Земле уже более 500 миллионов лет."},
	{"Lietuvių kalba – viena seniausių gyvų indoeuropiečių kalbų.",
		"Lithuanian is one of the oldest living Indo-European languages.",
		"Литовский – один из древнейших живых индоевропейских языков."},
	{"Medus nesugenda: archeologai rado tūkstančių metų senumo valgomo medaus.",
		"Honey does not spoil: archaeologists have found edible honey thousands of years old.",
		"Мёд не портится: археологи находили съедобный мёд возрастом в тысячи лет."},
	{"Aštuonkojis turi tris širdis ir mėlyną kraują.",
		"An octopus has three hearts and blue blood.",
		"У осьминога три сердца и голубая кровь."},
	{"Šviesa nuo Saulės iki Žemės keliauja maždaug 8 minutes ir 20 sekundžių.",
		"Sunlight takes about 8 minutes and 20 seconds to reach Earth.",
		"Свет от Солнца идёт до Земли примерно 8 минут 20 секунд."},
	{"Pirmoji kompiuterinė „klaida“ (bug) buvo tikras kandis, įstrigęs kompiuteryje 1947 metais.",
		"The first computer \"bug\" was a real moth stuck inside a computer in 1947.",
		"Первым компьютерным «багом» была настоящая моль, застрявшая в компьютере в 1947 году."},
	{"Lietuvos geografinis Europos centras yra netoli Vilniaus, Purnuškėse.",
		"The geographical centre of Europe is in Lithuania, near Vilnius, in Purnuškės.",
		"Географический центр Европы находится в Литве, рядом с Вильнюсом, в Пурнушкес."},
	{"Žmogaus kūne yra daugiau bakterijų ląstelių nei žmogaus ląstelių – arba bent jau panašiai.",
		"Your body holds about as many bacterial cells as human cells.",
		"В теле человека примерно столько же бактериальных клеток, сколько человеческих."},
	{"Bananai botaniškai yra uogos, o braškės – ne.",
		"Botanically, bananas are berries, but strawberries are not.",
		"С точки зрения ботаники бананы – ягоды, а клубника – нет."},
	{"Venera sukasi taip lėtai, kad para ten ilgesnė už metus.",
		"Venus spins so slowly that its day is longer than its year.",
		"Венера вращается так медленно, что сутки там длиннее года."},
	{"Kurmio urvai gali būti iki 100 metrų ilgio.",
		"A mole's tunnels can be up to 100 metres long.",
		"Норы крота могут достигать 100 метров в длину."},
	{"Pirmasis tinklalapis vis dar veikia – jis sukurtas 1991 metais CERN.",
		"The first website is still online – it was made at CERN in 1991.",
		"Первый веб-сайт до сих пор работает – он создан в CERN в 1991 году."},
	{"Kūdikiai gimsta turėdami apie 300 kaulų, o suaugusieji – 206.",
		"Babies are born with about 300 bones; adults have 206.",
		"Младенцы рождаются примерно с 300 костями, а у взрослых их 206."},
	{"Kai kurie vėžliai gali kvėpuoti per uodegos galą.",
		"Some turtles can breathe through their rear end.",
		"Некоторые черепахи могут дышать через заднюю часть тела."},
	{"Žemė kas sekundę skrieja aplink Saulę maždaug 30 kilometrų.",
		"Earth travels about 30 kilometres around the Sun every second.",
		"Земля каждую секунду пролетает вокруг Солнца около 30 километров."},
	{"Kurėnas – tradicinė Kuršių marių žvejų burvaltė su vėtrungėmis.",
		"The kurėnas is a traditional Curonian Lagoon fishing boat with carved weather vanes.",
		"Курен – традиционная рыбацкая лодка Куршского залива с резными флюгерами."},
	{"Varnos atpažįsta žmonių veidus ir gali juos prisiminti metų metus.",
		"Crows recognise human faces and can remember them for years.",
		"Вороны узнают человеческие лица и помнят их годами."},
	{"Šachmatų partijų variantų yra daugiau nei atomų stebimoje Visatoje.",
		"There are more possible chess games than atoms in the observable universe.",
		"Вариантов шахматных партий больше, чем атомов в наблюдаемой Вселенной."},
	{"Žirafos liežuvis gali būti apie 50 centimetrų ilgio.",
		"A giraffe's tongue can be about 50 centimetres long.",
		"Язык жирафа может быть около 50 сантиметров в длину."},
	{"Gintaras – tai suakmenėjusi medžių sakai, kuriems dažnai dešimtys milijonų metų.",
		"Amber is fossilised tree resin, often tens of millions of years old.",
		"Янтарь – это окаменевшая смола деревьев, часто возрастом в десятки миллионов лет."},
	{"Debesis gali sverti daugiau nei milijoną kilogramų.",
		"A cloud can weigh more than a million kilograms.",
		"Облако может весить более миллиона килограммов."},
	{"Sraigės gali miegoti iki trejų metų.",
		"Snails can sleep for up to three years.",
		"Улитки могут спать до трёх лет."},
	{"Pirmasis mobilusis skambutis buvo atliktas 1973 metais.",
		"The first mobile phone call was made in 1973.",
		"Первый звонок по мобильному телефону был сделан в 1973 году."},
	{"Delfinai miega užmerkę tik vieną akį – pusė smegenų visada budi.",
		"Dolphins sleep with one eye open – half of the brain stays awake.",
		"Дельфины спят с одним открытым глазом – половина мозга всегда бодрствует."},
}

// todaysFact picks the fact for the day in the current language.
func todaysFact(now time.Time) string {
	i := now.YearDay() % len(facts)
	switch curLang {
	case "en":
		return facts[i][1]
	case "ru":
		return facts[i][2]
	}
	return facts[i][0]
}
