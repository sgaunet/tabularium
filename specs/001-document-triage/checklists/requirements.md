# Specification Quality Checklist: Tri, nommage et archivage d'un document

**Purpose**: Validate specification completeness and quality before proceeding to planning
**Created**: 2026-08-30
**Feature**: [spec.md](../spec.md)

## Content Quality

- [x] No implementation details (languages, frameworks, APIs)
- [x] Focused on user value and business needs
- [x] Written for non-technical stakeholders
- [x] All mandatory sections completed

## Requirement Completeness

- [x] No [NEEDS CLARIFICATION] markers remain
- [x] Requirements are testable and unambiguous
- [x] Success criteria are measurable
- [x] Success criteria are technology-agnostic (no implementation details)
- [x] All acceptance scenarios are defined
- [x] Edge cases are identified
- [x] Scope is clearly bounded
- [x] Dependencies and assumptions identified

## Feature Readiness

- [x] All functional requirements have clear acceptance criteria
- [x] User scenarios cover primary flows
- [x] Feature meets measurable outcomes defined in Success Criteria
- [x] No implementation details leak into specification

## Notes

**Validation complète : 16 items sur 16 passent.** La spécification est prête pour `/speckit-plan`.

### Itération 1 — rédaction

66 exigences, 4 histoires utilisateur priorisées, 10 critères de succès. Deux marqueurs
[NEEDS CLARIFICATION] laissés sur les deux seuls points sans défaut raisonnable : l'origine du niveau
intermédiaire de l'arborescence, et l'extraction d'un identifiant depuis la sortie d'une commande
quelconque.

### Itération 2 — sortie structurée et vocabulaire fermé

Ajout de FR-022 à FR-031 après une remarque de l'auteur. Le trou était réel : la spec disait
« document structuré » sans jamais exiger ni du JSON, ni une contrainte de génération, ni un
vocabulaire borné. Les exigences ont été calibrées sur un essai mené contre la pile locale de
l'auteur, à trois niveaux de contrainte et avec le même modèle :

| Contrainte envoyée | Résultat observé |
|---|---|
| Aucune | Réponse en texte mis en forme, pas de JSON du tout |
| « réponds en JSON » | JSON valide, mais structure entièrement inventée par le modèle |
| Schéma + énumérations | Conforme : les clés du schéma, valeur d'énumération respectée |

D'où FR-023 (le schéma doit être transmis, pas seulement demandé) et FR-024 (un service qui n'accepte
pas la contrainte est une configuration invalide, pas une source d'échecs intermittents).

Le même essai a montré une limite que le schéma ne corrige pas : sur une facture de garage
automobile, le modèle a renvoyé une valeur d'énumération **valide mais fausse**. L'énumération protège
l'arborescence, elle ne garantit pas le bon rangement. D'où FR-029 (valeur de repli obligatoire),
SC-013, un cas limite dédié et une hypothèse qui l'énonce plutôt que de la laisser découvrir à
l'usage.

### Itération 3 — levée des deux marqueurs

- **FR-039** — le gabarit de chemin expose les métadonnées déduites et les composantes de la date,
  mais **aucun champ ne décrit un dossier**. Les niveaux d'arborescence sont écrits en clair dans le
  gabarit de chaque règle. Conséquence forte, reprise en SC-014 : l'ensemble des emplacements que
  l'outil peut créer se lit intégralement dans la configuration, et une erreur du modèle ne peut que
  déplacer un document d'un dossier déclaré à un autre, jamais en créer un nouveau. Le champ
  « catégorie » introduit à l'itération 2 a donc été retiré du jeu de métadonnées.
- **FR-057** — la commande externe est traitée comme une boîte noire : code de sortie et sortie brute
  tronquée sont enregistrés dans le fichier annexe, sans qu'aucun identifiant soit extrait ni aucun
  format présumé.

FR-030 et FR-038 ont été renforcés en conséquence : puisque les tags pilotent désormais la
correspondance des règles, un tag libre mal formulé fait échouer la règle et le document retombe sur
la règle par défaut — mal rangé, mais de façon visible et rattrapable.

Total final : **76 exigences**, numérotées séquentiellement.

### Sur le respect de la constitution

Les termes d'implémentation (langage, outils PDF, service de modèle, outil d'archivage tiers) sont
décrits par leur rôle plutôt que nommés. En revanche le contrat observable de la ligne de commande —
séparation des canaux, codes de sortie 0/1/2, sélection du format de sortie, respect de l'absence de
couleur, précédence de configuration — est conservé dans la spécification, la constitution le rangeant
explicitement du côté du comportement et non de la mise en œuvre.
