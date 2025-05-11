package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Colores para la consola
const (
	Reset  = "\033[0m"
	Red    = "\033[31m"
	Green  = "\033[32m"
	Yellow = "\033[33m"
	Blue   = "\033[34m"
	Cyan   = "\033[36m"
	White  = "\033[37m"
)

// ForexInfo representa la información de un tipo de cambio
type ForexInfo struct {
	Symbol        string
	Name          string
	Price         float64
	PreviousClose float64
	Change        float64
	ChangePercent float64
}

// StockInfo representa la información de una acción
type StockInfo struct {
	Symbol        string
	Name          string
	Price         float64
	PreviousClose float64
	Change        float64
	ChangePercent float64
	Volume        int64
	Market        string
}

// YahooResponse representa la respuesta de la API de Yahoo Finance
type YahooResponse struct {
	QuoteSummary struct {
		Result []struct {
			Price struct {
				RegularMarketPrice struct {
					Raw float64 `json:"raw"`
				} `json:"regularMarketPrice"`
				RegularMarketPreviousClose struct {
					Raw float64 `json:"raw"`
				} `json:"regularMarketPreviousClose"`
				RegularMarketVolume struct {
					Raw int64 `json:"raw"`
				} `json:"regularMarketVolume"`
				ShortName string `json:"shortName"`
				LongName  string `json:"longName"`
			} `json:"price"`
		} `json:"result"`
		Error *struct {
			Code        string `json:"code"`
			Description string `json:"description"`
		} `json:"error"`
	} `json:"quoteSummary"`
}

// HTTPClient con reintentos y timeouts
type HTTPClient struct {
	client http.Client
}

// NewHTTPClient crea un nuevo cliente HTTP con configuración optimizada
func NewHTTPClient() *HTTPClient {
	// Crear un transporte personalizado para configurar timeouts
	transport := &http.Transport{
		MaxIdleConns:       10,
		IdleConnTimeout:    30 * time.Second,
		DisableCompression: false,
		// Configurar proxy si es necesario:
		// Proxy: http.ProxyURL(proxyURL),
	}

	return &HTTPClient{
		client: http.Client{
			Timeout:   15 * time.Second,
			Transport: transport,
		},
	}
}

// GetWithRetry realiza una solicitud GET con reintentos
func (c *HTTPClient) GetWithRetry(url string, headers map[string]string) (*http.Response, error) {
	maxRetries := 3
	var resp *http.Response
	var err error

	for i := 0; i < maxRetries; i++ {
		if i > 0 {
			fmt.Printf("Reintento %d/%d para URL: %s\n", i+1, maxRetries, url)
		}

		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			fmt.Printf("Error al crear la solicitud: %v\n", err)
			return nil, err
		}

		// Configurar cookies para evitar detección de bot
		req.AddCookie(&http.Cookie{
			Name:  "B",
			Value: "59jd1o5g2nojr&b=3&s=ls",
		})

		// Agregar headers mejorados
		for key, value := range headers {
			req.Header.Add(key, value)
		}

		// Imprimir los headers para depuración
		if i == 0 {
			fmt.Println("Headers de la solicitud:")
			for key, values := range req.Header {
				fmt.Printf("  %s: %s\n", key, values)
			}
		}

		fmt.Printf("Realizando solicitud a: %s\n", url)
		resp, err = c.client.Do(req)

		if err != nil {
			fmt.Printf("Error en la solicitud HTTP: %v\n", err)
			// Esperar antes de reintentar
			waitTime := time.Duration(1<<uint(i)) * time.Second
			fmt.Printf("Esperando %v antes del siguiente reintento...\n", waitTime)
			time.Sleep(waitTime)
			continue
		}

		fmt.Printf("Respuesta recibida. Código de estado: %d\n", resp.StatusCode)

		if resp.StatusCode < 500 && resp.StatusCode != 401 {
			return resp, nil
		}

		// Si recibimos 401, probamos con otra URL alternativa
		if resp.StatusCode == 401 && i < maxRetries-1 {
			resp.Body.Close()

			// Si estamos probando v10, cambiar a v8
			if strings.Contains(url, "v10") {
				url = strings.Replace(url, "v10", "v8", 1)
				fmt.Printf("Cambiando a endpoint v8: %s\n", url)
				continue
			}
		}

		fmt.Printf("Error del servidor (código %d). Reintentando...\n", resp.StatusCode)
		resp.Body.Close()

		// Esperar antes de reintentar (backoff exponencial)
		waitTime := time.Duration(1<<uint(i)) * time.Second
		fmt.Printf("Esperando %v antes del siguiente reintento...\n", waitTime)
		time.Sleep(waitTime)
	}

	if err != nil {
		return nil, err
	}

	if resp != nil {
		return resp, fmt.Errorf("después de %d intentos, el último código de estado fue: %d", maxRetries, resp.StatusCode)
	}

	return nil, fmt.Errorf("después de %d intentos, no se pudo obtener una respuesta", maxRetries)
}

// Lista de símbolos de divisas
var forexSymbols = []map[string]string{
	{"symbol": "ARS=X", "name": "Dólar Oficial"},
	{"symbol": "EURARS=X", "name": "Euro"},
	// Agregamos alternativas por si alguno de los símbolos no funciona
	{"symbol": "USDARS=X", "name": "Dólar Oficial (alt)"},
	{"symbol": "EURUSD=X", "name": "Euro/USD"},
}

// Lista completa de ADRs argentinos en NYSE
var stocks = [][]string{
	// Bancos y Financieras
	{"GGAL", "NYSE"}, // Grupo Financiero Galicia
	{"BMA", "NYSE"},  // Banco Macro
	{"BBAR", "NYSE"}, // BBVA Banco Francés
	{"SUPV", "NYSE"}, // Grupo Supervielle
	{"BSMX", "NYSE"}, // Banco Santander México (relacionado con Argentina)

	// Energía y Petróleo
	{"YPF", "NYSE"}, // YPF
	{"PAM", "NYSE"}, // Pampa Energía
	{"EDN", "NYSE"}, // Edenor

	// Tecnología y Telecomunicaciones
	{"TEO", "NYSE"},  // Telecom Argentina
	{"GLOB", "NYSE"}, // Globant (tecnología)
	{"MELI", "NYSE"}, // MercadoLibre

	// Industria y Materiales
	{"TS", "NYSE"}, // Tenaris
	{"TX", "NYSE"}, // Ternium

	// Real Estate y Construcción
	{"IRS", "NYSE"},  // IRSA
	{"IRCP", "NYSE"}, // IRSA Propiedades Comerciales

	// Agricultura y Alimentos
	{"CRESY", "NYSE"}, // Cresud

	// Infraestructura y Transporte
	{"TGS", "NYSE"}, // Transportadora Gas del Sur
	{"VSH", "NYSE"}, // Vishay (con operaciones significativas en Argentina)
}

// ClearScreen limpia la pantalla de la consola
func clearScreen() {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", "cls")
	} else {
		cmd = exec.Command("clear")
	}
	cmd.Stdout = os.Stdout
	cmd.Run()
}

// GetTickerData obtiene los datos de un ticker con Yahoo Finance API
func getTickerData(symbol string, client *HTTPClient) (float64, float64, string, int64, error) {
	// Probamos primero con la API v8 que suele ser más estable
	url := fmt.Sprintf("https://query2.finance.yahoo.com/v8/finance/chart/%s", symbol)

	headers := map[string]string{
		"User-Agent":                "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/108.0.0.0 Safari/537.36",
		"Accept":                    "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8",
		"Accept-Language":           "en-US,en;q=0.5",
		"DNT":                       "1",
		"Connection":                "keep-alive",
		"Sec-Fetch-Dest":            "document",
		"Sec-Fetch-Mode":            "navigate",
		"Sec-Fetch-Site":            "none",
		"Cache-Control":             "no-cache",
		"Pragma":                    "no-cache",
		"Sec-Fetch-User":            "?1",
		"Upgrade-Insecure-Requests": "1",
		"Referer":                   "https://finance.yahoo.com/",
	}

	fmt.Printf("Consultando datos para %s...\n", symbol)
	resp, err := client.GetWithRetry(url, headers)
	if err != nil {
		// Si falla, intentamos con la API v10
		fmt.Printf("Intentando con API v10 para %s...\n", symbol)
		url = fmt.Sprintf("https://query1.finance.yahoo.com/v10/finance/quoteSummary/%s?modules=price", symbol)
		resp, err = client.GetWithRetry(url, headers)

		if err != nil {
			fmt.Printf("Error en la solicitud HTTP para %s: %v\n", symbol, err)
			return 0, 0, "", 0, err
		}
	}
	defer resp.Body.Close()

	// Verificar el código de estado
	if resp.StatusCode != http.StatusOK {
		return 0, 0, "", 0, fmt.Errorf("código de estado HTTP inesperado: %d para %s", resp.StatusCode, symbol)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("Error al leer el cuerpo de la respuesta para %s: %v\n", symbol, err)
		return 0, 0, "", 0, err
	}

	// Si es API v8 (chart), parseamos diferente
	if strings.Contains(url, "v8/finance/chart") {
		return parseV8Response(body, symbol)
	}

	// Si es API v10 (quoteSummary), usamos el parser original
	return parseV10Response(body, symbol)
}

// Parsea respuesta de la API v8 (chart)
func parseV8Response(body []byte, symbol string) (float64, float64, string, int64, error) {
	// Definir estructura para API v8
	var chartResp struct {
		Chart struct {
			Result []struct {
				Meta struct {
					RegularMarketPrice  float64 `json:"regularMarketPrice"`
					PreviousClose       float64 `json:"previousClose"`
					RegularMarketVolume int64   `json:"regularMarketVolume"`
					ExchangeName        string  `json:"exchangeName"`
					InstrumentType      string  `json:"instrumentType"`
					ShortName           string  `json:"shortName"`
				} `json:"meta"`
			} `json:"result"`
			Error *struct {
				Code        string `json:"code"`
				Description string `json:"description"`
			} `json:"error"`
		} `json:"chart"`
	}

	err := json.Unmarshal(body, &chartResp)
	if err != nil {
		fmt.Printf("Error al decodificar JSON v8 para %s: %v\n", symbol, err)
		return 0, 0, "", 0, err
	}

	// Verificar si hay error en la respuesta
	if chartResp.Chart.Error != nil {
		fmt.Printf("Error en la respuesta para %s: %s - %s\n",
			symbol,
			chartResp.Chart.Error.Code,
			chartResp.Chart.Error.Description)
		return 0, 0, "", 0, fmt.Errorf("%s: %s",
			chartResp.Chart.Error.Code,
			chartResp.Chart.Error.Description)
	}

	// Verificar que haya resultados
	if len(chartResp.Chart.Result) == 0 {
		fmt.Printf("No hay resultados disponibles para %s\n", symbol)
		return 0, 0, "", 0, fmt.Errorf("no data available for %s", symbol)
	}

	meta := chartResp.Chart.Result[0].Meta
	name := meta.ShortName
	if name == "" {
		name = symbol // Si no hay nombre, usamos el símbolo
	}

	fmt.Printf("Datos obtenidos para %s: precio=%f, previo=%f, nombre=%s\n",
		symbol, meta.RegularMarketPrice, meta.PreviousClose, name)

	return meta.RegularMarketPrice, meta.PreviousClose, name, meta.RegularMarketVolume, nil
}

// Parsea respuesta de la API v10 (quoteSummary)
func parseV10Response(body []byte, symbol string) (float64, float64, string, int64, error) {
	var yahooResp YahooResponse
	err := json.Unmarshal(body, &yahooResp)
	if err != nil {
		fmt.Printf("Error al decodificar JSON para %s: %v\n", symbol, err)
		return 0, 0, "", 0, err
	}

	if len(yahooResp.QuoteSummary.Result) == 0 {
		fmt.Printf("No hay resultados disponibles para %s\n", symbol)
		return 0, 0, "", 0, fmt.Errorf("no data available for %s", symbol)
	}

	price := yahooResp.QuoteSummary.Result[0].Price
	currentPrice := price.RegularMarketPrice.Raw
	previousClose := price.RegularMarketPreviousClose.Raw
	volume := price.RegularMarketVolume.Raw

	name := price.ShortName
	if name == "" {
		name = price.LongName
	}

	fmt.Printf("Datos obtenidos para %s: precio=%f, previo=%f, nombre=%s\n",
		symbol, currentPrice, previousClose, name)

	return currentPrice, previousClose, name, volume, nil
}

// GetForexData obtiene datos de tipos de cambio
func getForexData(client *HTTPClient) ([]ForexInfo, error) {
	var forexData []ForexInfo
	var wg sync.WaitGroup
	var mu sync.Mutex
	errorCh := make(chan error, len(forexSymbols))

	for _, forex := range forexSymbols {
		wg.Add(1)
		go func(symbol, name string) {
			defer wg.Done()
			currentPrice, previousClose, _, _, err := getTickerData(symbol, client)
			if err != nil {
				errorCh <- fmt.Errorf("error al obtener datos para %s: %v", symbol, err)
				return
			}

			change := currentPrice - previousClose
			changePercent := 0.0
			if previousClose != 0 {
				changePercent = (change / previousClose) * 100
			}

			mu.Lock()
			forexData = append(forexData, ForexInfo{
				Symbol:        symbol,
				Name:          name,
				Price:         currentPrice,
				PreviousClose: previousClose,
				Change:        change,
				ChangePercent: changePercent,
			})
			mu.Unlock()
		}(forex["symbol"], forex["name"])
	}

	wg.Wait()
	close(errorCh)

	// Procesar errores
	for err := range errorCh {
		fmt.Println(err)
	}

	return forexData, nil
}

// GetStockData obtiene datos actualizados de las acciones
func getStockData(dolarRate float64, client *HTTPClient) ([]StockInfo, error) {
	var stocksData []StockInfo
	var wg sync.WaitGroup
	var mu sync.Mutex
	errorCh := make(chan error, len(stocks))

	for _, stock := range stocks {
		symbol := stock[0]
		market := stock[1]

		wg.Add(1)
		go func(symbol, market string) {
			defer wg.Done()
			currentPrice, previousClose, name, volume, err := getTickerData(symbol, client)
			if err != nil {
				errorCh <- fmt.Errorf("error al obtener datos para %s: %v", symbol, err)
				return
			}

			change := currentPrice - previousClose
			changePercent := 0.0
			if previousClose != 0 {
				changePercent = (change / previousClose) * 100
			}

			// Convertir a pesos si tenemos la tasa de cambio y es del mercado NYSE
			if dolarRate != 0 && market == "NYSE" {
				currentPrice *= dolarRate
				change *= dolarRate
			}

			mu.Lock()
			stocksData = append(stocksData, StockInfo{
				Symbol:        symbol,
				Name:          name,
				Price:         currentPrice,
				PreviousClose: previousClose,
				Change:        change,
				ChangePercent: changePercent,
				Volume:        volume,
				Market:        market,
			})
			mu.Unlock()
		}(symbol, market)
	}

	wg.Wait()
	close(errorCh)

	// Procesar errores
	for err := range errorCh {
		fmt.Println(err)
	}

	return stocksData, nil
}

// DisplayStockRow muestra una fila de datos de acción con formato
func displayStockRow(stock StockInfo) {
	// Color según el cambio sea positivo o negativo
	changeColor := Red
	if stock.Change >= 0 {
		changeColor = Green
	}

	marketColor := Yellow
	if stock.Market != "NYSE" {
		marketColor = White
	}

	// Mostrar símbolo y nombre de la empresa
	fmt.Printf("%s%-10s%s", marketColor, stock.Symbol, Reset)

	name := stock.Name
	if len(name) > 30 {
		name = name[:30]
	}
	fmt.Printf("%s%-31s%s", Cyan, name, Reset)

	// Mostrar precio y cambios
	fmt.Printf("$%.2f ", stock.Price)
	fmt.Printf("%s%+.2f (%+.2f%%)%s", changeColor, stock.Change, stock.ChangePercent, Reset)
	fmt.Printf(" Vol: %d\n", stock.Volume)
}

// DisplayData muestra los datos en la consola con formato
func displayData(forexData []ForexInfo, stocksData []StockInfo) {
	clearScreen()
	fmt.Printf("\n%s=== TIPOS DE CAMBIO ===%s\n", Cyan, Reset)
	fmt.Printf("Actualizado: %s\n\n", time.Now().Format("2006-01-02 15:04:05"))

	if len(forexData) > 0 {
		for _, forex := range forexData {
			changeColor := Red
			if forex.Change >= 0 {
				changeColor = Green
			}

			fmt.Printf("%s%-12s%s", White, forex.Name, Reset)
			fmt.Printf("$%.2f ", forex.Price)
			fmt.Printf("%s%+.2f (%+.2f%%)%s\n", changeColor, forex.Change, forex.ChangePercent, Reset)
		}
	} else {
		fmt.Printf("%sNo hay datos disponibles de tipos de cambio%s\n", Red, Reset)
	}

	fmt.Printf("\n%s=== MERCADO DE VALORES ARGENTINO ===%s\n", Cyan, Reset)

	if len(stocksData) > 0 {
		// Filtrar y ordenar acciones NYSE
		var nyseStocks []StockInfo
		for _, stock := range stocksData {
			if stock.Market == "NYSE" {
				nyseStocks = append(nyseStocks, stock)
			}
		}

		// Ordenar por símbolo
		sort.Slice(nyseStocks, func(i, j int) bool {
			return nyseStocks[i].Symbol < nyseStocks[j].Symbol
		})

		fmt.Printf("\n%sAcciones argentinas en NYSE (en pesos)%s\n", Yellow, Reset)
		fmt.Printf("\n%sOrganizado por sectores:%s\n\n", White, Reset)

		for _, stock := range nyseStocks {
			displayStockRow(stock)
		}
	} else {
		fmt.Printf("\n%sNo hay datos disponibles del mercado de valores%s\n", Red, Reset)
	}

	fmt.Printf("\n%sPresiona Ctrl+C para detener el programa%s\n", Yellow, Reset)
}

// Intenta obtener datos para un símbolo individual como prueba
func testSymbol(symbol string, client *HTTPClient) {
	fmt.Printf("\n==== PROBANDO CONEXIÓN CON SÍMBOLO: %s ====\n", symbol)
	currentPrice, previousClose, name, volume, err := getTickerData(symbol, client)
	if err != nil {
		fmt.Printf("❌ Error al probar el símbolo %s: %v\n", symbol, err)
	} else {
		fmt.Printf("✅ Éxito para el símbolo %s:\n", symbol)
		fmt.Printf("   Nombre: %s\n", name)
		fmt.Printf("   Precio actual: %.2f\n", currentPrice)
		fmt.Printf("   Precio anterior: %.2f\n", previousClose)
		fmt.Printf("   Volumen: %d\n", volume)
	}
	fmt.Println("============================================")
}

func main() {
	fmt.Println("Iniciando monitoreo del mercado argentino y tipos de cambio...")

	// Crear cliente HTTP
	client := NewHTTPClient()

	// Canal para manejar la señal de interrupción (Ctrl+C)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Canal para salir del bucle principal
	done := make(chan bool)

	go func() {
		<-sigChan
		fmt.Println("\nMonitoreo finalizado.")
		done <- true
	}()

	// Realizar pruebas iniciales de conectividad
	fmt.Println("\n=== REALIZANDO PRUEBAS DE CONEXIÓN ===")
	// Probar un símbolo de Yahoo ampliamente conocido - Apple
	testSymbol("AAPL", client)
	// Probar un símbolo forex
	testSymbol("ARS=X", client)
	// Probar un símbolo argentino
	testSymbol("YPF", client)
	fmt.Println("=== FIN DE PRUEBAS DE CONEXIÓN ===\n")

	// Bucle principal de actualización
	go func() {
		for {
			fmt.Println("\n=== INICIANDO CICLO DE ACTUALIZACIÓN ===")
			// Obtener datos de forex primero para tener la tasa de cambio
			fmt.Println("Obteniendo datos de FOREX...")
			forexData, err := getForexData(client)
			if err != nil {
				fmt.Printf("\nError al obtener datos forex: %v\n", err)
				fmt.Println("Reintentando en 5 segundos...")
				time.Sleep(5 * time.Second)
				continue
			}

			fmt.Printf("Se obtuvieron %d registros de FOREX\n", len(forexData))

			// Obtener tasa de cambio del dólar si está disponible
			var dolarRate float64
			for _, forex := range forexData {
				if strings.Contains(forex.Name, "Dólar Oficial") {
					dolarRate = forex.Price
					fmt.Printf("Tasa de cambio del dólar: %.2f\n", dolarRate)
					break
				}
			}

			if dolarRate == 0 {
				fmt.Println("⚠️ No se pudo obtener la tasa del dólar oficial")
			}

			// Obtener datos de acciones
			fmt.Println("Obteniendo datos de acciones...")
			stocksData, err := getStockData(dolarRate, client)
			if err != nil {
				fmt.Printf("\nError al obtener datos de acciones: %v\n", err)
				fmt.Println("Reintentando en 5 segundos...")
				time.Sleep(5 * time.Second)
				continue
			}

			fmt.Printf("Se obtuvieron %d registros de acciones\n", len(stocksData))

			// Mostrar datos
			displayData(forexData, stocksData)

			// Esperar antes de la siguiente actualización
			fmt.Println("Esperando 5 segundos para la próxima actualización...")
			time.Sleep(5 * time.Second)
		}
	}()

	// Esperar señal de finalización
	<-done
}
