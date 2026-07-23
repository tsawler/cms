// Admin UI translation. English is the source language and also the key:
// every user-visible string is written in English at its call site and
// looked up in frStrings when the request's admin language is French. A
// missing key simply renders the English, so the UI can never go blank.
//
// The admin language is per-user, held in the cms_admin_lang cookie and
// switched with the topbar toggle (POST /lang). French is only ever
// offered when "fr" is one of the configured site locales.

package admin

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// langCookieName holds the user's admin UI language ("en" or "fr").
const langCookieName = "cms_admin_lang"

// frEnabled reports whether the site is configured with a French locale,
// which is what turns the French admin UI (and its toggle) on.
func (s *server) frEnabled() bool {
	for _, code := range s.deps.Locales {
		if code == "fr" {
			return true
		}
	}
	return false
}

// adminLang resolves the admin UI language for a request: the language
// cookie when set, otherwise the Accept-Language header, otherwise
// English. It only ever returns "fr" when the site has a French locale.
func (s *server) adminLang(r *http.Request) string {
	if !s.frEnabled() {
		return "en"
	}
	if c, err := r.Cookie(langCookieName); err == nil {
		switch c.Value {
		case "fr":
			return "fr"
		case "en":
			return "en"
		}
	}
	if acceptLanguagePrefersFrench(r.Header.Get("Accept-Language")) {
		return "fr"
	}
	return "en"
}

// acceptLanguagePrefersFrench reports whether an Accept-Language header
// ranks French above English (fr present and en absent counts too).
func acceptLanguagePrefersFrench(header string) bool {
	frQ, enQ := -1.0, -1.0
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lang, q := part, 1.0
		if i := strings.IndexByte(part, ';'); i >= 0 {
			lang = strings.TrimSpace(part[:i])
			for _, p := range strings.Split(part[i+1:], ";") {
				if v, ok := strings.CutPrefix(strings.TrimSpace(p), "q="); ok {
					if f, err := strconv.ParseFloat(v, 64); err == nil {
						q = f
					}
				}
			}
		}
		lang = strings.ToLower(lang)
		switch {
		case lang == "fr" || strings.HasPrefix(lang, "fr-"):
			frQ = max(frQ, q)
		case lang == "en" || strings.HasPrefix(lang, "en-"):
			enQ = max(enQ, q)
		}
	}
	return frQ > enQ
}

// tr translates a UI string for the request's admin language. The English
// text is the key; unknown keys come back unchanged.
func (s *server) tr(r *http.Request, key string) string {
	if s.adminLang(r) == "fr" {
		if v, ok := frStrings[key]; ok {
			return v
		}
	}
	return key
}

// T is tr for templates: {{.T "Save draft"}} renders the French when the
// request resolved to the French admin UI, the English key otherwise.
func (td templateData) T(key string) string {
	if td.AdminLang == "fr" {
		if v, ok := frStrings[key]; ok {
			return v
		}
	}
	return key
}

// setLang handles the topbar language toggle: it stores the chosen admin
// language in a long-lived cookie and sends the user back where they were.
// POST /lang  form value "to" = "en" | "fr"
func (s *server) setLang(w http.ResponseWriter, r *http.Request) {
	to := r.PostFormValue("to")
	if to != "fr" || !s.frEnabled() {
		to = "en"
	}
	http.SetCookie(w, &http.Cookie{
		Name:     langCookieName,
		Value:    to,
		Path:     "/",
		MaxAge:   int((365 * 24 * time.Hour).Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	dest := r.Header.Get("Referer")
	if !strings.HasPrefix(dest, "/") || strings.HasPrefix(dest, "//") {
		dest = s.deps.AdminPath + "/"
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

// frStrings maps every admin UI string (English, exactly as written at its
// call site) to its Canadian French translation. Grouped by the template or
// handler the string first appears in; strings shared across pages appear
// once, in their first group.
var frStrings = map[string]string{
	// Layout / navigation.
	"Dashboard":   "Tableau de bord",
	"Pages":       "Pages",
	"Blog & News": "Blogue et nouvelles",
	"Media":       "Médias",
	"Snippets":    "Blocs",
	"Users":       "Utilisateurs",
	"Log out":     "Se déconnecter",

	// Login page and handler.
	"Log in":      "Se connecter",
	"Email":       "Courriel",
	"Password":    "Mot de passe",
	"Remember me": "Se souvenir de moi",
	"for":         "pendant",
	"hours":       "heures",
	"Too many failed attempts. Please wait a few minutes and try again.": "Trop de tentatives échouées. Veuillez patienter quelques minutes et réessayer.",
	"That email and password combination didn't work.":                   "Cette combinaison de courriel et de mot de passe n'a pas fonctionné.",
	"Please complete the verification challenge.":                        "Veuillez compléter le test de vérification.",
	"Verification failed. Please try again.":                             "La vérification a échoué. Veuillez réessayer.",

	// Dashboard.
	"Welcome back,": "Bon retour,",
	"Create and edit the pages on your site.":                                      "Créez et modifiez les pages de votre site.",
	"Not configured — the host application must set TemplateFS and PageTemplates.": "Non configuré — l'application hôte doit définir TemplateFS et PageTemplates.",
	"Ready-made blocks editors can drop into pages.":                               "Des blocs prêts à l'emploi que les éditeurs peuvent insérer dans les pages.",
	"Add and manage the people who can edit this site.":                            "Ajoutez et gérez les personnes qui peuvent modifier ce site.",
	"Upload and manage the images used on your site.":                              "Téléversez et gérez les images utilisées sur votre site.",
	"Not configured — the host application must set Config.S3.":                    "Non configuré — l'application hôte doit définir Config.S3.",
	"Write and publish blog posts and news items.":                                 "Rédigez et publiez des billets de blogue et des nouvelles.",
	"Not configured — the host application must set Config.PostTemplate.":          "Non configuré — l'application hôte doit définir Config.PostTemplate.",

	// Users list.
	"New user":      "Nouvel utilisateur",
	"Name":          "Nom",
	"Role":          "Rôle",
	"Status":        "État",
	"active":        "actif",
	"inactive":      "inactif",
	"Edit":          "Modifier",
	"No users yet.": "Aucun utilisateur pour l'instant.",
	"editor":        "éditeur",
	"admin":         "admin",
	"superadmin":    "superadmin",

	// User form and handler.
	"Edit user":                                     "Modifier l'utilisateur",
	"Back to users":                                 "Retour aux utilisateurs",
	"New password":                                  "Nouveau mot de passe",
	"(leave blank to keep current)":                 "(laissez vide pour conserver l'actuel)",
	"Editor — can edit content":                     "Éditeur — peut modifier le contenu",
	"Admin — can also manage users":                 "Admin — peut aussi gérer les utilisateurs",
	"Superadmin — can also edit raw page HTML":      "Superadmin — peut aussi modifier le HTML brut des pages",
	"Active — inactive users cannot log in":         "Actif — les utilisateurs inactifs ne peuvent pas se connecter",
	"Create user":                                   "Créer l'utilisateur",
	"Save changes":                                  "Enregistrer les modifications",
	"That email address is already in use.":         "Cette adresse courriel est déjà utilisée.",
	"User created.":                                 "Utilisateur créé.",
	"User updated.":                                 "Utilisateur mis à jour.",
	"You cannot deactivate your own account.":       "Vous ne pouvez pas désactiver votre propre compte.",
	"You cannot remove your own admin role.":        "Vous ne pouvez pas retirer votre propre rôle d'admin.",
	"Name is required.":                             "Le nom est obligatoire.",
	"Email is required.":                            "Le courriel est obligatoire.",
	"That doesn't look like a valid email address.": "Cela ne ressemble pas à une adresse courriel valide.",
	"Choose a role.":                                "Choisissez un rôle.",
	"Password is required.":                         "Le mot de passe est obligatoire.",
	"Password must be at least 8 characters.":       "Le mot de passe doit contenir au moins 8 caractères.",

	// Pages list.
	"New page":   "Nouvelle page",
	"Title":      "Titre",
	"Address":    "Adresse",
	"Template":   "Gabarit",
	"published":  "publié",
	"draft":      "brouillon",
	"(untitled)": "(sans titre)",
	"No pages yet — create your first one.": "Aucune page pour l'instant — créez votre première.",

	// Page form.
	"Edit page":            "Modifier la page",
	"Preview draft":        "Aperçu du brouillon",
	"Back to pages":        "Retour aux pages",
	"Editing translation:": "Modification de la traduction :",
	"Fields left matching the default language keep showing it until you change them.": "Les champs identiques à la langue par défaut continuent de l'afficher tant que vous ne les modifiez pas.",
	"Address, template, and code settings live on the default-language tab.":           "L'adresse, le gabarit et les réglages de code se trouvent dans l'onglet de la langue par défaut.",
	"(leave empty for the homepage)":                                                   "(laissez vide pour la page d'accueil)",
	"about-us":                                                                         "a-propos",
	"Description":                                                                      "Description",
	"(for search engines)":                                                             "(pour les moteurs de recherche)",
	"Changing the template takes effect after saving; the content fields below will update to match.": "Le changement de gabarit prend effet après l'enregistrement; les champs de contenu ci-dessous se mettront à jour en conséquence.",
	"Advanced: page-specific CSS and JavaScript":                                                      "Avancé : CSS et JavaScript propres à cette page",
	"Extra CSS":                     "CSS supplémentaire",
	"(added to this page's <head>)": "(ajouté au <head> de cette page)",
	"Extra JavaScript":              "JavaScript supplémentaire",
	"(added before </body>)":        "(ajouté avant </body>)",
	"Create page":                   "Créer la page",
	"Save draft":                    "Enregistrer le brouillon",
	"Save & publish":                "Enregistrer et publier",
	"Unpublish":                     "Dépublier",
	"Discard your unpublished changes and revert to the published version? This can't be undone.": "Abandonner vos modifications non publiées et revenir à la version publiée? Cette action est irréversible.",
	"This page has unpublished draft changes.":                                                    "Cette page a des modifications de brouillon non publiées.",
	"Discard draft changes": "Abandonner les modifications du brouillon",
	"Delete this page and all of its content? This cannot be undone.": "Supprimer cette page et tout son contenu? Cette action est irréversible.",
	"Delete page": "Supprimer la page",

	// Pages handlers.
	"That address is already used by another page.":           "Cette adresse est déjà utilisée par une autre page.",
	"Page created — now add your content below.":              "Page créée — ajoutez maintenant votre contenu ci-dessous.",
	"Title is required.":                                      "Le titre est obligatoire.",
	"Page published.":                                         "Page publiée.",
	"Page unpublished — it is no longer visible on the site.": "Page dépubliée — elle n'est plus visible sur le site.",
	"Post published.":                                         "Billet publié.",
	"Post unpublished — it is no longer visible on the site.": "Billet dépublié — il n'est plus visible sur le site.",
	"Draft saved. Publish when you're ready to make it live.": "Brouillon enregistré. Publiez quand vous êtes prêt à le mettre en ligne.",
	"The home page can't be deleted.":                         "La page d'accueil ne peut pas être supprimée.",
	"Page deleted.":                                           "Page supprimée.",
	"There are no published changes to revert to — this page hasn't been published yet.": "Il n'y a aucune version publiée à restaurer — cette page n'a pas encore été publiée.",
	"Draft changes discarded — the editor now matches the published page.":               "Modifications du brouillon abandonnées — l'éditeur correspond maintenant à la page publiée.",
	"Use only lowercase letters, numbers, and hyphens, e.g. about-us.":                   "Utilisez seulement des lettres minuscules, des chiffres et des traits d'union, p. ex. a-propos.",
	"That address starts with a language code, which is reserved for translated pages.":  "Cette adresse commence par un code de langue, réservé aux pages traduites.",
	"Choose a template.": "Choisissez un gabarit.",

	// Posts list.
	"New post":                             "Nouveau billet",
	"All":                                  "Tous",
	"Blog":                                 "Blogue",
	"News":                                 "Nouvelles",
	"Feed":                                 "Fil",
	"Date":                                 "Date",
	"Author":                               "Auteur",
	"blog":                                 "blogue",
	"news":                                 "nouvelles",
	"No posts yet — write your first one.": "Aucun billet pour l'instant — rédigez votre premier.",

	// Post form.
	"Edit post":     "Modifier le billet",
	"Back to posts": "Retour aux billets",
	"Feed, address, date, and images live on the default-language tab.": "Le fil, l'adresse, la date et les images se trouvent dans l'onglet de la langue par défaut.",
	"(after /blog/ or /news/; leave empty to use the title)":            "(après /blog/ ou /news/; laissez vide pour utiliser le titre)",
	"my-first-post": "mon-premier-billet",
	"Summary":       "Résumé",
	"(shown in listings, feeds, and search engines)":                        "(affiché dans les listes, les fils et les moteurs de recherche)",
	"The date shown on the post and used to order listings — newest first.": "La date affichée sur le billet et utilisée pour ordonner les listes — du plus récent au plus ancien.",
	"Thumbnail":                                        "Vignette",
	"(optional — shown on listing cards)":              "(facultatif — affichée sur les cartes des listes)",
	"— no thumbnail —":                                 "— aucune vignette —",
	"— no header image —":                              "— aucune image d'en-tête —",
	"(current image)":                                  "(image actuelle)",
	"Header image":                                     "Image d'en-tête",
	"(optional — shown at the top of the post)":        "(facultatif — affichée en haut du billet)",
	"Choose from the":                                  "Choisissez dans la",
	"media library":                                    "médiathèque",
	"; upload new images there first.":                 "; téléversez-y d'abord les nouvelles images.",
	"Advanced: post-specific CSS and JavaScript":       "Avancé : CSS et JavaScript propres à ce billet",
	"(added to this post's <head>)":                    "(ajouté au <head> de ce billet)",
	"Create post":                                      "Créer le billet",
	"The post's body is best edited on the site: open": "Le corps du billet se modifie mieux sur le site : ouvrez",
	"and press":                                        "et appuyez sur",
	"to write in place with sections and snippets.":    "pour écrire directement avec les sections et les blocs.",
	"This post has unpublished draft changes.":         "Ce billet a des modifications de brouillon non publiées.",
	"Delete this post and all of its content? This cannot be undone.": "Supprimer ce billet et tout son contenu? Cette action est irréversible.",
	"Delete post": "Supprimer le billet",

	// Posts handlers.
	"That address is already used by another page or post.":                               "Cette adresse est déjà utilisée par une autre page ou un autre billet.",
	"Post created — now add your content below, or open it on the site to edit in place.": "Billet créé — ajoutez maintenant votre contenu ci-dessous, ou ouvrez-le sur le site pour le modifier directement.",
	"Post deleted.": "Billet supprimé.",
	"There are no published changes to revert to — this post hasn't been published yet.": "Il n'y a aucune version publiée à restaurer — ce billet n'a pas encore été publié.",
	"Draft changes discarded — the editor now matches the published post.":               "Modifications du brouillon abandonnées — l'éditeur correspond maintenant au billet publié.",
	"Choose Blog or News.": "Choisissez Blogue ou Nouvelles.",
	"Use only lowercase letters, numbers, and hyphens, e.g. my-first-post.": "Utilisez seulement des lettres minuscules, des chiffres et des traits d'union, p. ex. mon-premier-billet.",
	"Enter a valid date and time.":                                          "Entrez une date et une heure valides.",
	"Give the post a title.":                                                "Donnez un titre au billet.",
	"Creating the post failed — try again.":                                 "La création du billet a échoué — réessayez.",
	"Too many posts already use that name.":                                 "Trop de billets utilisent déjà ce nom.",
	"Saving the post settings failed — try again.":                          "L'enregistrement des réglages du billet a échoué — réessayez.",
	"Could not read the request — try again.":                               "Impossible de lire la requête — réessayez.",

	// Media page.
	"Description (alt text, for images)": "Description (texte alternatif, pour les images)",
	"No folder":                          "Aucun dossier",
	"Upload":                             "Téléverser",
	"Images (JPEG, PNG, GIF, WebP — resized automatically; SVG), videos (MP4, WebM — stored as uploaded), PDFs, office documents, text/CSV, or ZIP.": "Images (JPEG, PNG, GIF, WebP — redimensionnées automatiquement; SVG), vidéos (MP4, WebM — conservées telles quelles), PDF, documents bureautiques, texte/CSV ou ZIP.",
	"Images and documents up to 25 MB; videos up to": "Images et documents jusqu'à 25 Mo; vidéos jusqu'à",
	"MB.": "Mo.",
	"Everything is stored on your site's bucket.": "Tout est stocké dans le compartiment de votre site.",
	"Search by name…":                             "Rechercher par nom…",
	"All folders":                                 "Tous les dossiers",
	"Unfiled":                                     "Non classés",
	"Filter":                                      "Filtrer",
	"Clear":                                       "Effacer",
	"New folder name":                             "Nom du nouveau dossier",
	"Add folder":                                  "Ajouter un dossier",
	"Description (alt text)":                      "Description (texte alternatif)",
	"Save":                                        "Enregistrer",
	"Folder":                                      "Dossier",
	"Copy link":                                   "Copier le lien",
	"Delete this image? Pages still using it will show a broken picture.": "Supprimer cette image? Les pages qui l'utilisent encore afficheront une image brisée.",
	"Delete": "Supprimer",
	"No images yet — upload your first one above.": "Aucune image pour l'instant — téléversez votre première ci-dessus.",
	"No videos yet — upload an MP4 or WebM above.": "Aucune vidéo pour l'instant — téléversez un MP4 ou un WebM ci-dessus.",
	"Images":    "Images",
	"Grid view": "Vue en grille",
	"List view": "Vue en liste",
	"All media":     "Tous les médias",
	"Delete folder": "Supprimer le dossier",
	"folder":        "dossier",
	"file":          "fichier",
	"files":         "fichiers",
	"Search":        "Rechercher",
	"Search all folders by name…": "Rechercher par nom dans tous les dossiers…",
	"Videos": "Vidéos",
	"Delete this video? Pages still using it will show a broken player.": "Supprimer cette vidéo? Les pages qui l'utilisent encore afficheront un lecteur brisé.",
	"Documents": "Documents",
	"Size":      "Taille",
	"Delete this document? Pages that link to it will have a broken link.": "Supprimer ce document? Les pages qui pointent vers lui auront un lien brisé.",
	"No documents yet.": "Aucun document pour l'instant.",
	"Folders":           "Dossiers",
	"Files":             "Fichiers",
	"Delete this folder? Its files will be kept and become unfiled.": "Supprimer ce dossier? Ses fichiers seront conservés et deviendront non classés.",

	// Media handlers.
	"That file is too large — images and documents must be under %d MB, videos under %d MB.":                                          "Ce fichier est trop volumineux — les images et documents doivent faire moins de %d Mo, les vidéos moins de %d Mo.",
	"That file type isn't supported. Use an image (JPEG, PNG, GIF, WebP, SVG), video (MP4, WebM), PDF, office document, text/CSV, or ZIP.": "Ce type de fichier n'est pas pris en charge. Utilisez une image (JPEG, PNG, GIF, WebP, SVG), une vidéo (MP4, WebM), un PDF, un document bureautique, du texte/CSV ou un ZIP.",
	"That SVG can't be used — it contains scripts or other active content.":                                                                "Ce SVG ne peut pas être utilisé — il contient des scripts ou d'autres contenus actifs.",
	"Choose a file to upload.": "Choisissez un fichier à téléverser.",
	"Video uploaded.":          "Vidéo téléversée.",
	"Document uploaded.":       "Document téléversé.",
	"Image uploaded.":          "Image téléversée.",
	"Description saved.":       "Description enregistrée.",
	"Media deleted.":           "Média supprimé.",
	"Folder created.":          "Dossier créé.",
	"Folder deleted — its files are now unfiled.":       "Dossier supprimé — ses fichiers sont maintenant non classés.",
	"A folder with that name already exists.":           "Un dossier portant ce nom existe déjà.",
	"Folder names must be between 1 and 60 characters.": "Le nom d'un dossier doit compter entre 1 et 60 caractères.",

	// Snippets list.
	"New snippet": "Nouveau bloc",
	"Snippets are ready-made blocks editors can insert into pages from the editor's palette. Deleting one never changes pages that already use it.": "Les blocs sont des éléments prêts à l'emploi que les éditeurs peuvent insérer dans les pages depuis la palette de l'éditeur. En supprimer un ne change jamais les pages qui l'utilisent déjà.",
	"Updated":        "Mis à jour",
	"Section preset": "Préréglage de section",
	"Inline block":   "Bloc en ligne",
	"Delete this snippet? Pages already using it keep their copy.": "Supprimer ce bloc? Les pages qui l'utilisent déjà conservent leur copie.",
	"No custom snippets yet.":                                      "Aucun bloc personnalisé pour l'instant.",
	"Built-in snippets":                                            "Blocs intégrés",
	"These come from the site's code and can't be edited here.":    "Ceux-ci proviennent du code du site et ne peuvent pas être modifiés ici.",

	// Snippet form and handlers.
	"Edit snippet":     "Modifier le bloc",
	"Back to snippets": "Retour aux blocs",
	"Shown in the editor's palette, e.g. \"Pricing card\".": "Affiché dans la palette de l'éditeur, p. ex. « Carte de prix ».",
	"Type": "Type",
	"Inline block — inserted into existing content":       "Bloc en ligne — inséré dans le contenu existant",
	"Section preset — starting point for a whole section": "Préréglage de section — point de départ d'une section entière",
	"A section preset appears only in the editor's \"Add a section\" chooser and creates the section with the settings below already applied.": "Un préréglage de section n'apparaît que dans le sélecteur « Ajouter une section » de l'éditeur et crée la section avec les réglages ci-dessous déjà appliqués.",
	"Section settings":        "Réglages de la section",
	"Background style":        "Style d'arrière-plan",
	"Content width":           "Largeur du contenu",
	"Section height":          "Hauteur de la section",
	"Auto (fits the content)": "Auto (s'ajuste au contenu)",
	"50% of the screen":       "50 % de l'écran",
	"75% of the screen":       "75 % de l'écran",
	"Full screen":             "Plein écran",
	"Vertical alignment":      "Alignement vertical",
	"Top":                     "Haut",
	"Center":                  "Centre",
	"Bottom":                  "Bas",
	"The editor's per-section ⚙ dialog can still change all of these after the section is created.":                                                                                          "Le dialogue ⚙ de chaque section dans l'éditeur peut encore modifier tous ces réglages après la création de la section.",
	"Use the site's CSS classes (with Tailwind, remember to safelist any class that appears only in snippets). Avoid scripts and SVG — they are removed when editor-role users save a page.": "Utilisez les classes CSS du site (avec Tailwind, pensez à mettre sur la liste de sûreté toute classe qui n'apparaît que dans les blocs). Évitez les scripts et les SVG — ils sont retirés quand un utilisateur de rôle éditeur enregistre une page.",
	"Create snippet": "Créer le bloc",
	"Snippet created — it's now available in the editor's palette.": "Bloc créé — il est maintenant disponible dans la palette de l'éditeur.",
	"Snippet saved.": "Bloc enregistré.",
	"Snippet deleted. Copies already inserted into pages are unchanged.": "Bloc supprimé. Les copies déjà insérées dans des pages restent inchangées.",
	"The snippet needs some HTML.":                                       "Le bloc a besoin de HTML.",
	"Could not load snippets.":                                           "Impossible de charger les blocs.",

	// Shared form_regions partial.
	"Content": "Contenu",
	"This area is built from sections — open the page on the site and press": "Cette zone est constituée de sections — ouvrez la page sur le site et appuyez sur",
	"to add, arrange, and edit them directly.":                               "pour les ajouter, les organiser et les modifier directement.",
	"— no image —": "— aucune image —",
	"Image uploads are not configured for this site.": "Le téléversement d'images n'est pas configuré pour ce site.",

	// JSON API for the in-place editor.
	"Could not read the edit — try again.":     "Impossible de lire la modification — réessayez.",
	"Saving failed — try again.":               "L'enregistrement a échoué — réessayez.",
	"Give the page a name.":                    "Donnez un nom à la page.",
	"Choose a page type.":                      "Choisissez un type de page.",
	"Creating the page failed — try again.":    "La création de la page a échoué — réessayez.",
	"Too many pages already use that name.":    "Trop de pages utilisent déjà ce nom.",
	"Choose a translation language to revert.": "Choisissez une langue de traduction à réinitialiser.",
	"Reverting failed — try again.":            "La réinitialisation a échoué — réessayez.",
	"Could not load the pages.":                "Impossible de charger les pages.",
	"Unknown menu.":                            "Menu inconnu.",
	"Could not load the menu.":                 "Impossible de charger le menu.",
	"Every menu item needs a label.":           "Chaque élément de menu a besoin d'un libellé.",
	"Custom links need a web address like https://… or a path like /contact.": "Les liens personnalisés ont besoin d'une adresse web comme https://… ou d'un chemin comme /contact.",
	"Could not read the menu — try again.":                                    "Impossible de lire le menu — réessayez.",
	"Too many menu items.":                                                    "Trop d'éléments de menu.",
	"Only dropdown items can hold other items.":                               "Seuls les éléments déroulants peuvent contenir d'autres éléments.",
	"Dropdown menus can only go one level deep.":                              "Les menus déroulants ne peuvent avoir qu'un seul niveau.",
	"One of the linked pages no longer exists — reload and try again.":        "Une des pages liées n'existe plus — rechargez et réessayez.",
	"Saving the menu failed — try again.":                                     "L'enregistrement du menu a échoué — réessayez.",
	"Could not load the site settings.":                                       "Impossible de charger les paramètres du site.",
	"Unknown menu alignment.":                                                 "Alignement de menu inconnu.",
	"The site name is too long.":                                              "Le nom du site est trop long.",
	"The logo needs to be an uploaded image or a web address.":                "Le logo doit être une image téléversée ou une adresse web.",
	"Saving the site settings failed — try again.":                            "L'enregistrement des paramètres du site a échoué — réessayez.",
	"Deleting the page failed — try again.":                                   "La suppression de la page a échoué — réessayez.",
	"Too many sections on one page.":                                          "Trop de sections sur une même page.",
	"Unknown sections area.":                                                  "Zone de sections inconnue.",
	"Publishing failed — try again.":                                          "La publication a échoué — réessayez.",
	"Discarding failed — try again.":                                          "L'abandon a échoué — réessayez.",
	"Unknown media kind.":                                                     "Type de média inconnu.",
	"Could not load the media library.":                                       "Impossible de charger la médiathèque.",
	"That file could not be processed.":                                       "Ce fichier n'a pas pu être traité.",
	"Could not load folders.":                                                 "Impossible de charger les dossiers.",
	"Could not read the folder name.":                                         "Impossible de lire le nom du dossier.",
	"Could not create the folder.":                                            "Impossible de créer le dossier.",
}
