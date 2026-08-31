package api

import (
	"fmt"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/marion909/voltpanel/internal/core"
)

// maxUploadBytes deckelt eine einzelne hochgeladene Datei.
const maxUploadBytes = 512 << 20 // 512 MiB

// Alle Datei-Endpunkte arbeiten mit einer site_id und einem Pfad relativ dazu.
// Ein absoluter Pfad aus dem Browser käme hier nie an — er wäre eine Einladung,
// ihn zu manipulieren.

func (s *Server) handleFileList(c echo.Context) error {
	siteID, err := pathID(c)
	if err != nil {
		return err
	}
	entries, err := s.files.List(c.Request().Context(), currentScope(c), siteID, c.QueryParam("path"))
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, map[string]any{"path": c.QueryParam("path"), "entries": entries})
}

func (s *Server) handleFileRead(c echo.Context) error {
	siteID, err := pathID(c)
	if err != nil {
		return err
	}
	content, err := s.files.Read(c.Request().Context(), currentScope(c), siteID, c.QueryParam("path"))
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, map[string]string{"path": c.QueryParam("path"), "content": content})
}

type filePathBody struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Recursive bool   `json:"recursive"`
}

func (s *Server) handleFileWrite(c echo.Context) error {
	siteID, err := pathID(c)
	if err != nil {
		return err
	}
	var body filePathBody
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	ctx := c.Request().Context()
	if err := s.files.Write(ctx, currentScope(c), siteID, body.Path, body.Content); err != nil {
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "file.write", "file", body.Path, "ok", c.RealIP(),
		map[string]int64{"site": siteID})
	return c.NoContent(http.StatusNoContent)
}

func (s *Server) handleFileMkdir(c echo.Context) error {
	siteID, err := pathID(c)
	if err != nil {
		return err
	}
	var body filePathBody
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}
	if err := s.files.Mkdir(c.Request().Context(), currentScope(c), siteID, body.Path); err != nil {
		return storeError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (s *Server) handleFileDelete(c echo.Context) error {
	siteID, err := pathID(c)
	if err != nil {
		return err
	}
	var body filePathBody
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	ctx := c.Request().Context()
	if err := s.files.Remove(ctx, currentScope(c), siteID, body.Path, body.Recursive); err != nil {
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "file.delete", "file", body.Path, "ok", c.RealIP(),
		map[string]any{"site": siteID, "rekursiv": body.Recursive})
	return c.NoContent(http.StatusNoContent)
}

type fileMoveBody struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func (s *Server) handleFileMove(c echo.Context) error {
	siteID, err := pathID(c)
	if err != nil {
		return err
	}
	var body fileMoveBody
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}
	if err := s.files.Move(c.Request().Context(), currentScope(c), siteID, body.From, body.To); err != nil {
		return storeError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (s *Server) handleFileCopy(c echo.Context) error {
	siteID, err := pathID(c)
	if err != nil {
		return err
	}
	var body fileMoveBody
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}
	if err := s.files.Copy(c.Request().Context(), currentScope(c), siteID, body.From, body.To); err != nil {
		return storeError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

type fileChmodBody struct {
	Path      string `json:"path"`
	Mode      string `json:"mode"` // oktal als Text, z.B. "0644"
	Recursive bool   `json:"recursive"`
}

func (s *Server) handleFileChmod(c echo.Context) error {
	siteID, err := pathID(c)
	if err != nil {
		return err
	}
	var body fileChmodBody
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	mode, err := strconv.ParseUint(strings.TrimPrefix(body.Mode, "0o"), 8, 32)
	if err != nil || mode == 0 || mode > 0o777 {
		return echo.NewHTTPError(http.StatusBadRequest,
			"modus muss oktal zwischen 001 und 777 liegen, z.B. 644")
	}

	if err := s.files.Chmod(c.Request().Context(), currentScope(c), siteID,
		body.Path, uint32(mode), body.Recursive); err != nil {
		return storeError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

type archiveBody struct {
	Sources []string `json:"sources"`
	Dest    string   `json:"dest"`
	Archive string   `json:"archive"`
}

func (s *Server) handleFileArchive(c echo.Context) error {
	siteID, err := pathID(c)
	if err != nil {
		return err
	}
	var body archiveBody
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	size, err := s.files.Archive(c.Request().Context(), currentScope(c), siteID, body.Sources, body.Dest)
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, map[string]any{"dest": body.Dest, "size_bytes": size})
}

func (s *Server) handleFileExtract(c echo.Context) error {
	siteID, err := pathID(c)
	if err != nil {
		return err
	}
	var body archiveBody
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	count, err := s.files.Extract(c.Request().Context(), currentScope(c), siteID, body.Archive, body.Dest)
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, map[string]any{"dest": body.Dest, "entries": count})
}

// handleFileDownload streamt die Datei blockweise zum Browser.
func (s *Server) handleFileDownload(c echo.Context) error {
	siteID, err := pathID(c)
	if err != nil {
		return err
	}
	rel := c.QueryParam("path")

	ctx := c.Request().Context()
	info, err := s.files.Stat(ctx, currentScope(c), siteID, rel)
	if err != nil {
		return storeError(err)
	}
	if info.IsDir {
		return echo.NewHTTPError(http.StatusBadRequest,
			"verzeichnisse lassen sich nicht direkt laden — bitte erst archivieren")
	}

	// Der Dateiname geht in einen Header. mime.FormatMediaType kodiert ihn
	// korrekt, statt ihn roh einzusetzen — sonst ließe sich über einen
	// Dateinamen mit Anführungszeichen ein weiterer Header anhängen.
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": info.Name})
	h := c.Response().Header()
	h.Set(echo.HeaderContentDisposition, disposition)
	h.Set(echo.HeaderContentLength, strconv.FormatInt(info.Size, 10))
	// Immer als Download ausliefern: eine hochgeladene .html-Datei darf im
	// Kontext des Panels nicht als Seite gerendert werden.
	h.Set(echo.HeaderContentType, "application/octet-stream")
	h.Set("X-Content-Type-Options", "nosniff")
	c.Response().WriteHeader(http.StatusOK)

	if _, err := s.files.Download(ctx, currentScope(c), siteID, rel, c.Response()); err != nil {
		// Die Kopfzeilen sind raus; ein sauberer Fehlerstatus geht nicht mehr.
		s.log.Warn("download abgebrochen", "site", siteID, "pfad", rel, "err", err)
	}
	return nil
}

// handleFileUpload nimmt eine Datei per multipart entgegen und schreibt sie
// blockweise durch — der Web-Prozess hält sie nie ganz im Speicher.
func (s *Server) handleFileUpload(c echo.Context) error {
	siteID, err := pathID(c)
	if err != nil {
		return err
	}

	file, err := c.FormFile("file")
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "es wurde keine datei übertragen")
	}
	if file.Size > maxUploadBytes {
		return echo.NewHTTPError(http.StatusRequestEntityTooLarge,
			fmt.Sprintf("datei ist größer als %d bytes", int64(maxUploadBytes)))
	}

	src, err := file.Open()
	if err != nil {
		return err
	}
	defer src.Close()

	// Nur der Basisname des Uploads zählt; ein Browser könnte in einem
	// Verzeichnis-Upload sonst Pfadanteile mitschicken.
	name := path.Base(strings.ReplaceAll(file.Filename, `\`, "/"))
	if name == "." || name == "/" || name == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "der dateiname ist ungültig")
	}
	target := path.Join(c.FormValue("path"), name)

	ctx := c.Request().Context()
	written, err := s.files.Upload(ctx, currentScope(c), siteID, target, src,
		core.UploadOptions{Size: file.Size, MaxBytes: maxUploadBytes})
	if err != nil {
		return storeError(err)
	}

	s.audit(ctx, currentUser(c), "file.upload", "file", target, "ok", c.RealIP(),
		map[string]any{"site": siteID, "bytes": written})
	return c.JSON(http.StatusCreated, map[string]any{"path": target, "size_bytes": written})
}
