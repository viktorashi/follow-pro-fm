Ok defapt, cauta pe github sau pe alte code forgeuri sau ceva sa vezi daca a mai facut deja cineva cheastia asta, sau macar super blank slate de poti tu sa-ti punctele de unde sa asculti, ce sa trimiti and whatever.

Daca nu:

O scriem in golang.
Campanie o sa fie de fiecare data cate 2 saptamani de luni până vineri, în intervalul orar 07:00-20:00, deci 13 ore pe zi, trebuie sa ruleze programul, cate 130 de ore pe timpul cerlor 2 saptmanai. In fiecare zi o sa fie 6 oportunitati, deci daca au trecut 6, trebuie sa te cam opresti si sa te trezesti pe data aviatoare.
4 sesiune deci 520 de ore de rulare overall (cu gapuri, nu constanta, but still). Trebuie sa te gandesti la asta.
Plus alte cateva ore de mine cum il testez.

```regulament
5.1.1 În cele patru perioade de desfășurare a concursului „PRO FM și Karpaten Turism te trimit la
concerte în Europa!” vor avea loc zilnic, de luni până vineri, în intervalul orar 07:00-20:00, câte 6
sesiuni de înscriere în concurs (în total 240 de sesiuni), după cum urmează:
 În perioada 25 mai – 5 iunie 2026, se vor organiza zilnic, de luni până vineri câte 6 sesiuni de
înscriere în concurs (60 de sesiuni în total), iar extragerea și desemnarea câștigătorului primei
perioade de concurs va avea loc în data de 8 iunie 2026,
 În perioada 15 – 26 iunie 2026, se vor organiza zilnic, de luni până vineri câte 6 sesiuni de
înscriere în concurs (60 de sesiuni în total), iar extragerea și desemnarea câștigătorului celei
de-a doua perioade de concurs va avea loc în data de 29 iunie 2026,
 În perioada 20 – 31 iulie 2026, se vor organiza zilnic, de luni până vineri câte 6 sesiuni de
înscriere în concurs (60 de sesiuni în total), iar extragerea și desemnarea câștigătorului celei
de-a treia perioade de concurs va avea loc în data de 3 august 2026,
 În perioada 10 – 21 august 2026, se vor organiza zilnic, de luni până vineri câte 6 sesiuni de
înscriere în concurs (60 de sesiuni în total), iar extragerea și desemnarea câștigătorului celei
de-a patra perioade de concurs va avea loc în data de 24 august 2026.
```

Deci sa fie super fault tolerant:

Indiferent daca isi da datele despre ce se vorbeste la radio direct de pe stream audio sau dintrun stream text: trebuie sa vezi care sunt frazele speciale, si daca se aud [dupa alt pas intermediar bazat alte detalii de la mai sciru eu aici jos ],si dupa sa trimiti inscrierea (whatsapp API).

cauti in regulament daca poti sa trimiti mai multe inscrieri si daca t-i le ia in considerare chiar daca ai trimis cu alarama falsa: gen shazamu de ruleaza in cloud ti-a detectat corect o piesa de la artistii din capanie, doar ca defapt prezentatorul nu a zis de dinainte ca "asta e piesa de concurs". Daca nu te lasa sa faci asa ceva trebuie neaprat

if presenter.made_announcement && playing_song.artist.id in tracking_artists_ids

daca nu e problema cu asta tho, putem sa facem un, doar ca sa fim on the safe side

if presenter.made_announcement || playing_song.artist.id in tracking_artists_ids

Noi facem un mesaj audio gata de trimis de dinainte, care se trimite automat daca se afla asta.

Doar ca vine si problema: trebuie researchuit daca se stie ca in mesajul vocal audio respectiv trebuie sa se auda pe fundal piesa respectivca, ca sa se stie ca tu chiar o ascultai?

dam track la taote melodiile artistilor, nu doar la anumite. Singurul avantaj pe care o sa-l aiba alea mai populare o sa fie pt ca o sa avem deja audio pre-recorded pentru ele (daca chiar e nevoie sa-l ai in fundal, again)

Caz in care trebuie folosit un SDK pentru editare audio automata. Noi avem deja audiouri pre-recorded in care se aud in fundal cele mai populare piese care pot aparea acolo in companie, dar daca piesa care chiar se aude nu se afla printre alea pre-recorded ale noastre, trebuie downloadata si editat automat alt clip (blanc, in care doar ne zicem numele si de unde suntem, dar fara poza in fundal) cu noi astfel incat sa se auda, dupa editarea automata cu ceva sound editing SDK in go (daca are, daca nu, schimbam libaju, dar sa fie unu robust si type-safe, NU PYTHON) sa se auda in surdina. Dar trebuie researchuit

Daca incepe sa se auda intro-ul prezentatoului, incepi sa pui in overdrive super mare (daca exista setari de efficience vs performance pentru el) "shazamu" sau ACRcloudu ce o fi, ca sa poti sa vezi EXACT ce piesa urmeaza,sa trimiti inscrierea. Ca si cum il "activezi" mai tare

Pune toata chesita asta intr-un plan de research una dupa alta: dela: la ce date livestream avem la dispozitie?
Pana la regulamentul aferent INANTE SA ASCRIEM O SINGURA LINIE DE COD.

OOh sau poate e ceva metadata de ce piesa e live suuuper buna si nu-ti trebuie niciun shzam asa ar fi perfect. Trbuie testat ce latecy chiar are. Pentru ca aparent s-a facut deaj

Bazat pe ce date live avem sa le putem folosi robust (fara web-scrapere) care ne pot da block, de acolo stim de unde plecam.

Sunt o mie de variante in care putem merge, but what pins it all down, e: care e cel mai simplu ieftin rapid si effective mode in care putem, nu doar sa auzim piesa cand vine, dar SI sintagme specifice INAINTE sa vina sa stim sa ne "pregatim" mai bine sistemul ca URMEAZA AIA MARE. Acum depinzand ce fel de date live avem, e foarte posibil nici macar sa nu avem ce "pregati" si sa ruleze totul la fel, but you get what im saying?

Also trebuie stiuta politica lor cu false alarms, i dont wanna get disqualified.

HAHHAH <http://edge126.rdsnet.ro:84/profm/profm.mp3>

Prima data facem totul local, cu teste si simulation environment de cum ar fi CHIAR FIX in production rulat.

Si dupa ce rezolvam totul tot, deabea dupa ne gandim sa il punem pe vreun cloud
