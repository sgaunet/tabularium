# Feature Specification: Tri, nommage et archivage d'un document

**Feature Branch**: `main` (aucune branche dédiée : aucun hook git n'est installé)

**Created**: 2026-08-30

**Status**: Draft

**Input**: User description: "Tabularium : une CLI Go mono-tâche qui prend UN document en argument, en
extrait le texte (OCR par LLM local ou distant selon la configuration), en déduit des tags et un nom
de fichier plus parlant, le classe dans une arborescence locale selon des règles configurables, puis
le transmet à un outil d'archivage externe."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Comprendre un document et voir où il irait (Priority: P1)

Sylvain vient de scanner une facture de garage. Le fichier s'appelle `facture.pdf` et ne dit rien de
son contenu. Il lance `tabularium --dry-run facture.pdf` et obtient, sans qu'aucun octet ne soit
écrit : le texte reconnu, un titre, un type, un correspondant, des tags, une date de document, le nom
de fichier proposé et le chemin de destination calculé avec la règle qui l'a produit.

**Why this priority**: C'est la valeur irréductible de l'outil. Sans elle, rien d'autre n'a de sens ;
avec elle seule, l'outil sert déjà à identifier une pile de scans anonymes et à roder les règles de
classement avant de laisser quoi que ce soit bouger sur le disque.

**Independent Test**: Lancer la commande sur un PDF scanné et sur un PDF né numérique, vérifier que
la sortie décrit correctement le document dans les deux cas, et vérifier qu'aucun fichier n'a été
créé, déplacé ou modifié.

**Acceptance Scenarios**:

1. **Given** un PDF scanné sans couche texte, **When** l'utilisateur lance la commande, **Then** le
   texte est reconnu par le modèle vision et les métadonnées en sont déduites.
2. **Given** un PDF né numérique portant une couche texte, **When** l'utilisateur lance la commande,
   **Then** le texte est lu directement et aucun appel de reconnaissance visuelle n'est effectué.
3. **Given** une photo de ticket au format JPEG, **When** l'utilisateur lance la commande, **Then**
   le texte est reconnu et les métadonnées en sont déduites.
4. **Given** un document bureautique, **When** l'utilisateur lance la commande, **Then** le texte est
   extrait localement sans aucun appel de reconnaissance visuelle.
5. **Given** un fichier d'un type non pris en charge, **When** l'utilisateur lance la commande,
   **Then** le programme sort en erreur d'usage en nommant le type détecté.
6. **Given** une exécution en mode plan, **When** elle se termine, **Then** l'arborescence est
   octet pour octet identique à ce qu'elle était avant.

---

### User Story 2 - Classer le document dans l'arborescence (Priority: P2)

Sylvain lance `tabularium facture.pdf`. Le fichier quitte sa boîte à scans et réapparaît sous
`.../factures/voiture/2025/2025-03-14-facture-voiture.pdf`. Un fichier annexe déposé à côté enregistre
d'où il vient et ce qui a été compris de lui.

**Why this priority**: C'est le geste que l'outil automatise. Il dépend entièrement de l'histoire 1
et n'a de sens qu'une fois les règles rodées.

**Independent Test**: Configurer deux règles et une règle par défaut, lancer la commande sur trois
documents de types différents, vérifier que chacun atterrit sous le chemin dicté par la première
règle qui lui correspond et que la boîte à scans est vide.

**Acceptance Scenarios**:

1. **Given** une liste de règles dont deux correspondent au document, **When** le classement a lieu,
   **Then** c'est la première des deux dans l'ordre du fichier qui décide du chemin.
2. **Given** un document qu'aucune règle ne décrit et une règle par défaut configurée, **When** le
   classement a lieu, **Then** le document est rangé selon la règle par défaut.
3. **Given** une date de document extraite, **When** le nom est calculé, **Then** il commence par
   cette date au format `AAAA-MM-JJ`.
4. **Given** aucune date de document extraite, **When** le nom est calculé, **Then** il ne porte
   aucun préfixe de date.
5. **Given** un fichier déjà présent à destination avec un contenu identique, **When** le classement
   a lieu, **Then** rien n'est copié, le doublon est signalé et le programme sort en succès.
6. **Given** un fichier déjà présent à destination avec un contenu différent, **When** le classement
   a lieu, **Then** un suffixe numérique est ajouté et les deux fichiers coexistent.
7. **Given** une destination sur un autre volume que la source, **When** le déplacement a lieu,
   **Then** il aboutit malgré tout et la source disparaît seulement une fois la copie vérifiée.
8. **Given** une interruption au clavier pendant la copie, **When** le programme s'arrête, **Then**
   aucun fichier partiel ne subsiste à destination.

---

### User Story 3 - Transmettre le document à un archivage externe (Priority: P3)

Une fois le document classé, Sylvain veut qu'il parte aussi vers son outil d'archivage, avec le titre,
le type, le correspondant et les tags déjà déduits, sans avoir à les ressaisir.

**Why this priority**: C'est un multiplicateur de valeur, pas un prérequis. L'outil reste utile sans
lui, et l'étape dépend d'un programme tiers dont l'absence ne doit pas empêcher le reste.

**Independent Test**: Configurer une commande externe factice qui enregistre les arguments reçus,
lancer la commande sur un document, vérifier que les métadonnées déduites lui parviennent une à une
et que le résultat est enregistré.

**Acceptance Scenarios**:

1. **Given** une commande externe configurée, **When** le document a été classé, **Then** la commande
   est appelée avec les métadonnées déduites et le chemin du fichier archivé.
2. **Given** plusieurs tags déduits, **When** la commande est appelée, **Then** chaque tag lui parvient
   comme un argument distinct.
3. **Given** l'archivage externe désactivé par drapeau, **When** le document est classé, **Then**
   aucune commande externe n'est lancée.
4. **Given** une commande externe qui dépasse son délai, **When** le délai expire, **Then** le
   processus et ses enfants sont arrêtés et l'échec est signalé.
5. **Given** un jeton d'authentification configuré, **When** la commande est lancée, **Then** il lui
   parvient par l'environnement et n'apparaît jamais dans la liste des processus.

---

### User Story 4 - Rattraper un archivage externe qui a échoué (Priority: P4)

Le réseau était coupé : le document est bien rangé, mais l'archivage externe a échoué et le programme
l'a dit. Sylvain relance `tabularium` sur le fichier désormais archivé ; l'outil comprend que le
classement est déjà fait et ne rejoue que l'envoi.

**Why this priority**: C'est le confort qui rend le mode de défaillance choisi vivable. Sans lui, un
rattrapage reste possible à la main ; avec lui, il devient une relance de la même commande.

**Independent Test**: Faire échouer volontairement la commande externe, vérifier que le document est
classé et que le programme sort en erreur, puis relancer avec une commande externe fonctionnelle et
vérifier qu'aucune reconnaissance de texte n'est refaite et qu'aucun fichier n'est déplacé.

**Acceptance Scenarios**:

1. **Given** un classement réussi et un archivage externe en échec, **When** le programme se termine,
   **Then** le fichier reste à sa place d'archive, l'erreur part sur le canal de diagnostic et le
   programme sort en échec d'exécution.
2. **Given** un fichier déjà archivé accompagné de son fichier annexe, **When** la commande est
   relancée dessus, **Then** ni la reconnaissance de texte ni l'analyse ni le classement ne sont
   refaits.
3. **Given** un fichier annexe indiquant un archivage externe déjà réussi, **When** la commande est
   relancée, **Then** rien n'est renvoyé et le programme sort en succès.
4. **Given** un fichier annexe illisible, **When** la commande est relancée, **Then** il est traité
   comme absent, l'anomalie est signalée, et le document est retraité depuis le début.

---

### Edge Cases

- Un PDF de deux cents pages dépasse le plafond de pages : les pages au-delà ne sont pas lues, et la
  troncature est **enregistrée et visible en sortie**, jamais silencieuse.
- Un PDF est protégé par mot de passe : l'échec nomme la cause plutôt que de laisser croire à un
  document vide.
- Un document ne contient aucun texte reconnaissable (page blanche, photo floue) : le programme
  échoue explicitement au lieu de classer un document sur des métadonnées inventées.
- L'endpoint du modèle est injoignable, lent, ou renvoie une erreur : les tentatives sont bornées et
  espacées, puis le programme échoue en le disant.
- Le modèle renvoie une réponse qui n'est pas du JSON valide, ou à laquelle il manque des champs :
  rien n'est classé sur cette base.
- Le service accepte le schéma mais ne l'honore pas : la validation locale rattrape l'écart, la
  contrainte de génération n'étant jamais tenue pour acquise.
- Le modèle renvoie un type **du** vocabulaire mais **faux** — une facture de garage annoncée comme
  relevé bancaire. L'énumération garantit la validité, jamais la justesse : le document part au
  mauvais endroit sans que rien ne signale l'erreur. Seule une exécution en mode plan permet de le
  constater avant écriture.
- Le modèle produit le bon type mais formule un tag autrement qu'attendu — « automobile » là où la
  règle attend « voiture ». Aucune règle spécifique ne correspond, et le document retombe sur la règle
  par défaut : il est mal rangé, mais de façon visible et rattrapable, jamais dans un dossier inventé.
- Le vocabulaire des types ne comporte aucune valeur de repli, et aucune de ses valeurs ne décrit le
  document.
- Le modèle propose un nom de fichier contenant des séparateurs de chemin ou des composants relatifs :
  le nom est assaini avant tout usage, et ne peut jamais désigner un emplacement hors de la racine.
- Le modèle propose un nom vide, ou vide après assainissement : le nom d'origine est conservé.
- Un document bureautique est une archive piégée qui se décompresse en plusieurs gigaoctets :
  l'extraction est bornée et abandonne proprement.
- Le disque de destination est plein, ou le dossier n'est pas accessible en écriture : l'échec le dit
  et la source n'est pas perdue.
- Aucune règle ne correspond et aucune règle par défaut n'est configurée.
- Deux exécutions simultanées visent la même destination.
- La sortie n'est pas un terminal : ni couleur, ni animation, ni barre de progression.
- Le programme reçoit une demande d'arrêt pendant un appel au modèle, pendant une copie, ou pendant
  l'appel à la commande externe.

## Requirements *(mandatory)*

### Functional Requirements

#### Invocation et entrées

- **FR-001**: Le programme MUST accepter exactement un chemin de fichier en argument ; zéro argument
  ou plus d'un MUST produire une erreur d'usage.
- **FR-002**: Le type du document MUST être déterminé par examen de son contenu, jamais par son
  extension.
- **FR-003**: Le programme MUST accepter les documents PDF, les images matricielles (PNG, JPEG, TIFF,
  WebP), les documents bureautiques (traitement de texte, tableur, présentation, aux formats ouverts
  comme hérités) ainsi que le texte brut et le Markdown.
- **FR-004**: Un type non pris en charge MUST produire une erreur d'usage nommant le type détecté.
- **FR-005**: Un fichier introuvable, illisible, ou qui n'est pas un fichier ordinaire, MUST produire
  une erreur d'usage.

#### Extraction du texte

- **FR-006**: Pour un PDF, le programme MUST d'abord tenter de lire une couche texte existante ; si
  elle porte du texte, aucun appel de reconnaissance visuelle MUST être effectué.
- **FR-007**: Un PDF sans couche texte exploitable MUST voir ses pages converties en images, chaque
  page donnant lieu à un appel au modèle.
- **FR-008**: Le nombre de pages traitées MUST être plafonné par la configuration ; les pages au-delà
  du plafond MUST être abandonnées, et cette troncature MUST être enregistrée et rapportée en sortie.
- **FR-009**: Une image matricielle MUST être soumise directement au modèle multimodal.
- **FR-010**: Un document bureautique MUST voir son texte extrait localement, sans aucun appel de
  reconnaissance visuelle.
- **FR-011**: Un fichier texte ou Markdown MUST être lu directement.
- **FR-012**: Le service de reconnaissance MUST pouvoir être local ou distant ; les deux cas MUST se
  distinguer uniquement par la configuration, sans changement de comportement ni de code appelé.
- **FR-013**: Les outils externes nécessaires au traitement des PDF MUST être requis uniquement quand
  un PDF est traité ; leur absence MUST produire un message qui les nomme, jamais un échec obscur, et
  cette dépendance MUST être documentée dans l'aide en ligne.
- **FR-014**: L'extraction depuis un document bureautique MUST être bornée en taille décompressée et
  en profondeur, de sorte qu'une archive piégée ne puisse épuiser la machine.
- **FR-015**: Un document dont aucun texte n'a pu être extrait MUST produire un échec explicite, et
  MUST NOT être classé.

#### Analyse

- **FR-016**: Le texte extrait MUST être soumis à un second appel, purement textuel, qui renvoie un
  document JSON portant : titre, type, correspondant, tags, date du document, date d'échéance,
  référence, description, montant, devise et nom de fichier proposé. Le modèle MUST NOT produire de
  chemin, de dossier ni de niveau d'arborescence : l'emplacement est décidé par les règles seules.
- **FR-017**: La reconnaissance et l'analyse MUST être configurables indépendamment l'une de l'autre,
  chacune avec son service, son modèle, son secret d'authentification et son délai.
- **FR-018**: Une réponse d'analyse malformée, non conforme au schéma, ou incomplète MUST donner lieu
  à un nombre borné de nouvelles tentatives, puis à un échec ; aucun classement MUST résulter de
  cette base.
- **FR-019**: L'analyse MUST pouvoir être désactivée par drapeau, auquel cas seul le texte extrait est
  produit en sortie.
- **FR-020**: Une date qui n'est pas au format `AAAA-MM-JJ` MUST être rejetée plutôt que propagée.
- **FR-021**: Un montant sans devise, ou une devise sans montant, MUST voir les deux champs écartés.

#### Sortie structurée et vocabulaire fermé

- **FR-022**: La réponse de l'analyse MUST être du JSON conforme à un schéma déclaré. Du texte libre,
  du JSON entouré de balises de code, ou un objet dont la forme diffère du schéma MUST être traités
  comme une réponse malformée.
- **FR-023**: La requête MUST transmettre ce schéma au service, de sorte que la conformité soit
  contrainte au moment de la génération et non simplement demandée en langage naturel. Demander « du
  JSON » dans le texte de la requête MUST NOT être considéré comme suffisant.
- **FR-024**: Un service ou un modèle qui n'accepte pas cette contrainte de schéma MUST être signalé
  comme configuration invalide, par une erreur d'usage qui le nomme, plutôt que de produire des échecs
  intermittents en cours d'exécution.
- **FR-025**: La réponse reçue MUST être validée localement contre le schéma. Le respect de la
  contrainte par le service MUST NOT être tenu pour acquis.
- **FR-026**: Le type du document MUST être borné par un vocabulaire fermé déclaré dans la
  configuration. C'est le seul champ énuméré que le modèle renseigne, et il ne désigne pas un dossier :
  il alimente les conditions des règles.
- **FR-027**: Ce vocabulaire MUST être transmis au modèle comme l'énumération des seules valeurs
  acceptables du champ, et non comme une suggestion rédigée.
- **FR-028**: Une valeur reçue hors du vocabulaire MUST être rejetée localement, et MUST NOT créer de
  nouvelle branche dans l'arborescence.
- **FR-029**: Le vocabulaire des types MUST comporter une valeur de repli explicite, retenue lorsque
  le document ne relève clairement d'aucune autre, afin qu'un document mal compris soit reconnaissable
  comme tel plutôt que rangé à un mauvais endroit plausible.
- **FR-030**: Les tags MUST pouvoir être, au choix de la configuration, bornés par un vocabulaire
  fermé ou laissés libres. Comme une règle peut conditionner sur un tag, un tag laissé libre que le
  modèle formule autrement qu'attendu MUST faire échouer la correspondance de cette règle et non
  produire un rangement approximatif : le document retombe alors sur la règle par défaut.
- **FR-031**: Les valeurs reçues MUST être normalisées avant tout usage : dates ramenées au format
  `AAAA-MM-JJ` ou écartées, tags mis en minuscules et dédoublonnés, montant séparé de sa devise.

#### Nommage

- **FR-032**: Le nom proposé par le modèle MUST toujours être assaini avant usage : translittération
  vers l'ASCII, passage en minuscules, espaces convertis en tirets, ponctuation retirée, longueur
  bornée.
- **FR-033**: Le nom assaini MUST NOT contenir de séparateur de chemin ni de composant relatif ; s'il
  est vide après assainissement, le nom d'origine MUST être conservé.
- **FR-034**: Quand une date de document a été extraite, le nom MUST commencer par cette date au
  format `AAAA-MM-JJ`.
- **FR-035**: Quand aucune date de document n'a été extraite, le préfixe de date MUST être omis ; la
  date du jour MUST NOT lui être substituée.
- **FR-036**: L'extension d'origine MUST être conservée.

#### Classement local

- **FR-037**: Les règles de classement MUST former une liste ordonnée, et la première règle qui
  correspond au document MUST décider seule du chemin.
- **FR-038**: Une règle MUST pouvoir conditionner sur le type, sur la présence d'un ou plusieurs
  tags, et sur le correspondant ; une règle sans condition MUST servir de règle par défaut.
- **FR-039**: Le chemin de destination MUST être produit par un gabarit exposant les métadonnées
  déduites — type, correspondant, référence, titre — ainsi que les composantes de la date du document
  (année, mois, jour) et la liste des tags. Le gabarit MUST NOT disposer d'un champ décrivant un
  dossier : les niveaux d'arborescence sont écrits en clair dans le gabarit de chaque règle, de sorte
  que l'ensemble des emplacements possibles se lise dans la configuration.
- **FR-040**: Si aucune règle ne correspond et qu'aucune règle par défaut n'est configurée, le
  programme MUST échouer en nommant le document et MUST NOT le déplacer.
- **FR-041**: Le chemin résolu MUST rester confiné sous la racine d'archive configurée ; toute
  résolution qui en sortirait MUST être refusée.
- **FR-042**: Le devenir de la source MUST être configurable entre déplacement, copie et conservation
  sans écriture, le déplacement étant le comportement par défaut.
- **FR-043**: Un mode plan MUST forcer l'absence de toute écriture, quel que soit le réglage, et MUST
  afficher le nom calculé, le chemin de destination, la règle appliquée et les métadonnées déduites.
- **FR-044**: Le déplacement MUST aboutir même lorsque la source et la destination sont sur des
  volumes différents ; la source MUST NOT être supprimée avant que la copie ait été vérifiée.
- **FR-045**: Les dossiers intermédiaires du chemin de destination MUST être créés au besoin.
- **FR-046**: L'écriture à destination MUST être atomique du point de vue d'un observateur : aucun
  fichier partiel MUST être visible sous son nom définitif.
- **FR-047**: Une interruption pendant l'écriture MUST NOT laisser de résidu partiel à destination.

#### Collisions

- **FR-048**: Si le chemin de destination est déjà occupé, le contenu existant MUST être comparé à
  celui de la source par empreinte cryptographique.
- **FR-049**: Un contenu identique MUST être traité comme un doublon : rien MUST NOT être copié, le fait
  MUST être rapporté, et le programme MUST sortir en succès.
- **FR-050**: Un contenu différent MUST donner lieu à l'ajout d'un suffixe numérique, les deux
  fichiers coexistant.

#### Trace du traitement

- **FR-051**: Un fichier annexe MUST être déposé auprès du fichier archivé, portant l'empreinte de la
  source, les métadonnées déduites, l'horodatage du classement, l'état de l'archivage externe et
  l'éventuelle troncature.
- **FR-052**: Relancer le programme sur un fichier déjà archivé accompagné de son annexe MUST éviter
  la reconnaissance de texte, l'analyse et le classement, et MUST ne rejouer que l'archivage externe
  encore manquant.
- **FR-053**: Un fichier annexe absent, illisible ou incohérent avec le fichier qu'il accompagne MUST
  être traité comme absent, l'anomalie étant signalée.

#### Archivage externe

- **FR-054**: La commande d'archivage externe MUST être entièrement décrite par la configuration :
  programme à lancer et arguments à lui passer. Le programme MUST NOT présumer d'un outil particulier.
- **FR-055**: Les métadonnées déduites MUST être injectées dans les arguments par gabarit, chaque
  argument étant transmis comme un élément distinct de la ligne de commande, jamais par
  l'intermédiaire d'un interpréteur de commandes.
- **FR-056**: L'appel externe MUST avoir un délai explicite ; à son expiration, le processus et ses
  descendants MUST être arrêtés.
- **FR-057**: Le fichier annexe MUST enregistrer le code de sortie de la commande externe et sa sortie
  brute, tronquée à une taille bornée. Le programme MUST NOT chercher à en extraire un identifiant :
  il ne présume d'aucun format, et la trace reste lisible par un humain.
- **FR-057a**: La sortie de la commande externe MUST être répercutée sur le canal de diagnostic au
  fil de son arrivée, chaque ligne étant préfixée du nom de la commande dont elle provient. Le mode
  silencieux MAY la supprimer quand la commande réussit, mais MUST NOT la supprimer quand elle
  échoue : ce que la commande a dit est la seule chose qui indique quoi corriger.
- **FR-058**: Les secrets destinés à la commande externe MUST lui parvenir par l'environnement et
  MUST NOT figurer dans la ligne de commande.
- **FR-059**: Un code de sortie non nul de la commande externe MUST être traité comme un échec de
  l'étape et enregistré dans le fichier annexe.
- **FR-060**: Une commande externe introuvable MUST produire un message qui la nomme.

#### Activation et échecs partiels

- **FR-061**: Le classement local et l'archivage externe MUST être actifs par défaut dès lors qu'ils
  sont configurés.
- **FR-062**: Chacun MUST pouvoir être désactivé indépendamment par un drapeau.
- **FR-063**: Un classement réussi suivi d'un archivage externe en échec MUST laisser le fichier
  classé, signaler l'erreur sur le canal de diagnostic, et sortir en échec d'exécution.

#### Sortie et interface

- **FR-064**: La sortie standard MUST porter les données seules, sous une forme sélectionnable entre
  texte et JSON.
- **FR-065**: En mode texte, la sortie MUST être un tableau aligné sans bordures résumant la source,
  la destination, le type et les tags.
- **FR-066**: En mode JSON, la sortie MUST porter la source, la destination, la règle appliquée, les
  métadonnées déduites, l'éventuelle troncature et le résultat de l'archivage externe.
- **FR-067**: Les journaux, les erreurs et la progression MUST aller sur le canal de diagnostic, et
  MUST NOT atteindre la sortie standard.
- **FR-068**: Les codes de sortie MUST être `0` en succès, `1` en échec d'exécution et `2` en erreur
  d'usage ; ils MUST être documentés dans l'aide en ligne et couverts par un test.
- **FR-069**: Un doublon détecté MUST produire un code de sortie de succès.
- **FR-070**: Le programme MUST respecter la demande d'absence de couleur portée par l'environnement,
  et MUST NOT émettre de couleur, d'animation ni de barre de progression quand sa sortie n'est pas un
  terminal.
- **FR-071**: Le programme MUST offrir un mode silencieux et un mode détaillé.
- **FR-072**: La configuration MUST se résoudre dans l'ordre drapeaux, environnement, fichier de
  configuration, valeurs par défaut, et cet ordre MUST être énoncé dans l'aide en ligne.
- **FR-073**: Un secret d'authentification MUST NOT apparaître dans les journaux, dans les messages
  d'erreur, ni dans la ligne de commande d'un processus.

#### Fiabilité

- **FR-074**: Toute opération longue MUST être annulable, et le programme MUST s'arrêter proprement
  sur demande d'interruption, en laissant l'arborescence dans un état cohérent.
- **FR-075**: Toute opération d'entrée-sortie MUST avoir un délai explicite.
- **FR-076**: Les nouvelles tentatives MUST être en nombre borné et espacées.

### Key Entities

- **Document source**: le fichier soumis. Porte un chemin, un type déterminé par son contenu, une
  taille et une empreinte cryptographique qui l'identifie indépendamment de son nom.
- **Texte extrait**: le contenu textuel du document, accompagné de son origine (couche texte,
  reconnaissance visuelle, extraction bureautique, lecture directe) et de l'indication d'une
  éventuelle troncature.
- **Métadonnées déduites**: titre, type, correspondant, tags, date du document, date d'échéance,
  référence, description, montant, devise et nom de fichier proposé. Aucune d'elles ne nomme un
  dossier.
- **Vocabulaire**: l'ensemble fermé des valeurs qu'un champ peut prendre, déclaré en configuration.
  Celui des types comporte une valeur de repli. Sert à la fois à contraindre la génération et à
  valider la réponse.
- **Règle de classement**: une condition sur les métadonnées et un gabarit de chemin. Ordonnée par
  rapport aux autres ; la première qui correspond l'emporte.
- **Plan de classement**: le résultat du calcul avant toute écriture — nom assaini, chemin de
  destination, règle retenue, devenir de la source.
- **Fichier annexe**: la trace déposée auprès du fichier archivé — empreinte de la source,
  métadonnées déduites, horodatage, état de l'archivage externe, troncature.
- **Résultat d'archivage externe**: l'état de l'appel à la commande externe — succès ou échec, code
  de sortie, cause de l'échec, et sortie brute de la commande, tronquée.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Un document déjà porteur de son texte est classé sans qu'aucun appel de reconnaissance
  visuelle soit émis — mesurable en comptant les requêtes reçues par le service.
- **SC-002**: Un scan anonyme d'une page devient un fichier nommé et rangé au bon endroit en une seule
  invocation, sans aucune saisie de l'utilisateur.
- **SC-003**: Rejouer la même commande sur un document déjà traité ne crée aucun second fichier et
  sort en succès.
- **SC-004**: Après un échec de l'archivage externe, une unique relance de la même commande termine le
  travail sans refaire ni la reconnaissance de texte ni le classement.
- **SC-005**: La sortie JSON se lit intégralement par un analyseur syntaxique strict, sur chacun des
  types de documents pris en charge : aucun diagnostic ne pollue le canal de données.
- **SC-006**: Une interruption au clavier à n'importe quel instant du traitement laisse zéro fichier
  partiel à destination et zéro source perdue.
- **SC-007**: Toute exécution sur un type non pris en charge sort en erreur d'usage en nommant le type
  détecté — jamais en échec d'exécution, jamais en succès.
- **SC-008**: L'intégralité de la sortie d'une exécution en mode détaillé, canaux de données et de
  diagnostic réunis, ne contient aucun secret d'authentification.
- **SC-009**: Une exécution en mode plan laisse l'arborescence octet pour octet identique.
- **SC-010**: Un document de plus de pages que le plafond configuré produit une sortie qui signale
  explicitement la troncature ; l'information n'est jamais perdue.
- **SC-011**: Aucun document n'est classé sur des métadonnées non validées : le nombre de documents
  rangés à partir d'une réponse non conforme au schéma est nul.
- **SC-012**: Aucune exécution ne crée de branche d'arborescence en dehors du vocabulaire déclaré,
  quelle que soit la réponse du modèle — y compris lorsque le service ignore la contrainte de schéma.
- **SC-013**: Un document que le modèle ne sait pas typer atterrit dans l'emplacement dicté par la
  règle par défaut, et jamais dans un type choisi au hasard parmi les autres.
- **SC-014**: L'ensemble des emplacements que l'outil peut créer se lit intégralement dans la
  configuration : aucun dossier produit par une exécution n'est absent des gabarits déclarés.

## Assumptions

- La configuration est un fichier unique dans le répertoire de configuration de l'utilisateur, et tout
  secret qu'elle porte est surchargeable par une variable d'environnement.
- Désactiver l'analyse désactive de fait le classement et l'archivage externe : sans métadonnées,
  aucune règle ne peut correspondre et aucun nom ne peut être calculé. L'exécution produit alors le
  texte extrait et rien d'autre.
- Le fichier annexe est écrit à chaque classement réussi, et pas seulement lorsqu'une étape a échoué :
  c'est la trace du traitement, pas un journal d'erreurs.
- La langue des tags, du titre et du nom de fichier suit celle du document ; le nom de fichier est
  translittéré en ASCII quelle que soit cette langue.
- Les outils de traitement des PDF sont fournis par l'hôte et non embarqués. Ils sont présents sur la
  machine de développement.
- Le classement est déterministe : une fois les métadonnées connues, la règle retenue et le chemin
  produit ne dépendent d'aucun appel réseau et sont reproductibles.
- Le modèle chargé de l'analyse doit savoir produire une sortie contrainte par un schéma. Ce n'est pas
  une préférence de mise en œuvre mais un prérequis, vérifié sur la pile locale de l'auteur : sans
  contrainte, le modèle répond en texte mis en forme ; avec la seule consigne de répondre en JSON, il
  produit du JSON valide mais invente sa propre structure ; seule la contrainte par schéma produit les
  champs demandés.
- Une énumération contraint la forme de la réponse, pas sa justesse. Un modèle peut renvoyer une
  valeur parfaitement valide et pourtant fausse ; le vocabulaire fermé protège l'arborescence, il ne
  garantit pas le bon rangement.
- Le modèle ne décide jamais d'un emplacement. Il produit un type pris dans un vocabulaire fermé et
  des tags ; l'arborescence est écrite en clair dans les gabarits des règles. Une erreur du modèle
  déplace donc un document d'un dossier déclaré à un autre dossier déclaré, et ne peut jamais en créer
  un nouveau.
- Borner aussi le vocabulaire des tags est vivement conseillé dès qu'une règle conditionne sur un tag :
  un tag libre formulé autrement qu'attendu ne fait échouer que la correspondance, mais fait s'accumuler
  les documents sur la règle par défaut.
- La commande d'archivage externe est traitée comme une boîte noire : son code de sortie fait foi, et
  sa sortie est conservée telle quelle. Retrouver un document dans l'outil externe se fait en relisant
  cette trace, non par un identifiant que le programme aurait su interpréter.
- Le comportement de reconnaissance visuelle reprend celui, déjà éprouvé en production, d'un outil
  existant du même auteur : un appel par page, plafond de pages, troncature enregistrée, et un service
  compatible avec la même interface de conversation.
- Aucune branche git dédiée n'a été créée pour cette spécification : aucun hook git n'est installé
  dans ce dépôt.
