package server

import "net/http"

// These thin wrappers connect the routes registered in server.go to the PDF
// generation functions implemented in export.go.

func (s *Server) handleExportOverview(w http.ResponseWriter, r *http.Request) {
	s.exportOverviewPDF(w, r)
}

func (s *Server) handleExportStatistics(w http.ResponseWriter, r *http.Request) {
	s.exportStatisticsPDF(w, r)
}

func (s *Server) handleExportExpenses(w http.ResponseWriter, r *http.Request) {
	s.exportExpensesPDF(w, r)
}
