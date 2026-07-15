package editor

import "testing"

// corpus is 200 famous equations written the way you'd type them into a Math
// leaf (unicode operators and symbols). It is the coverage net for the type: the
// test below converts each to LaTeX and asserts every symbol mapped to ASCII —
// any unmapped operator/symbol fails loudly with its codepoint, which is how new
// operators get added. It also checks the coloring vocabulary keeps pace.
var corpus = []struct{ name, expr string }{
	// ── algebra, analysis, number theory ──────────────────────────────────
	{"pythagoras", "a² + b² = c²"},
	{"quadratic formula", "x = (-b ± √(b²-4ac)) / 2a"},
	{"difference of squares", "a² - b² = (a+b)(a-b)"},
	{"binomial square", "(a+b)² = a² + 2ab + b²"},
	{"cube sum", "a³ + b³ = (a+b)(a²-ab+b²)"},
	{"binomial theorem", "(x+y)ⁿ = Σ C(n,k) xⁿ⁻ᵏ yᵏ"},
	{"geometric series", "Σ arⁿ = a/(1-r)"},
	{"arithmetic series", "Σ k = n(n+1)/2"},
	{"sum of squares", "Σ k² = n(n+1)(2n+1)/6"},
	{"basel problem", "Σ 1/n² = π²/6"},
	{"euler identity", "e^(iπ) + 1 = 0"},
	{"euler formula", "e^(iθ) = cos θ + i sin θ"},
	{"golden ratio", "φ = (1 + √5)/2"},
	{"harmonic diverges", "Σ 1/n → ∞"},
	{"factorial", "n! = n · (n-1)!"},
	{"stirling", "n! ≈ √(2πn) (n/e)ⁿ"},
	{"logarithm product", "log(xy) = log x + log y"},
	{"change of base", "logₐ x = ln x / ln a"},
	{"exponential limit", "e = lim (1 + 1/n)ⁿ"},
	{"gamma factorial", "Γ(n) = (n-1)!"},
	{"euler product", "ζ(s) = ∏ 1/(1 - p⁻ˢ)"},
	{"riemann zeta", "ζ(s) = Σ 1/nˢ"},
	{"fermat last", "aⁿ + bⁿ ≠ cⁿ"},
	{"fermat little", "a^(p-1) ≡ 1"},
	{"euler totient", "φ(n) = n ∏ (1 - 1/p)"},
	{"gauss sum", "Σ k = n(n+1)/2"},
	{"triangle inequality", "|a + b| ≤ |a| + |b|"},
	{"cauchy schwarz", "|⟨u,v⟩| ≤ ‖u‖ ‖v‖"},
	{"am gm", "(a+b)/2 ≥ √(ab)"},
	{"de moivre", "(cos θ + i sin θ)ⁿ = cos nθ + i sin nθ"},
	{"complex modulus", "|z| = √(a² + b²)"},
	{"conjugate", "z z* = |z|²"},
	{"roots of unity", "zⁿ = 1"},
	{"partial fractions", "1/(x(x+1)) = 1/x - 1/(x+1)"},
	{"telescoping", "Σ (1/k - 1/(k+1)) = 1"},
	{"catalan", "Cₙ = C(2n,n)/(n+1)"},
	{"fibonacci", "Fₙ = Fₙ₋₁ + Fₙ₋₂"},
	{"binet", "Fₙ = (φⁿ - ψⁿ)/√5"},
	{"pascal rule", "C(n,k) = C(n-1,k-1) + C(n-1,k)"},
	{"vieta sum", "x₁ + x₂ = -b/a"},

	// ── geometry & trigonometry ──────────────────────────────────────────
	{"circle area", "A = π r²"},
	{"circle circumference", "C = 2π r"},
	{"sphere volume", "V = 4/3 π r³"},
	{"sphere area", "A = 4π r²"},
	{"cylinder volume", "V = π r² h"},
	{"cone volume", "V = 1/3 π r² h"},
	{"triangle area", "A = 1/2 b h"},
	{"herons formula", "A = √(s(s-a)(s-b)(s-c))"},
	{"law of cosines", "c² = a² + b² - 2ab cos γ"},
	{"law of sines", "a/sin α = b/sin β"},
	{"pythagorean identity", "sin²θ + cos²θ = 1"},
	{"tan identity", "1 + tan²θ = sec²θ"},
	{"double angle sin", "sin 2θ = 2 sin θ cos θ"},
	{"double angle cos", "cos 2θ = cos²θ - sin²θ"},
	{"angle sum sin", "sin(α+β) = sin α cos β + cos α sin β"},
	{"euler polyhedron", "V - E + F = 2"},
	{"arc length", "s = r θ"},
	{"sector area", "A = 1/2 r² θ"},
	{"distance formula", "d = √((x₂-x₁)² + (y₂-y₁)²)"},
	{"slope", "m = (y₂-y₁)/(x₂-x₁)"},
	{"ellipse", "x²/a² + y²/b² = 1"},
	{"hyperbola", "x²/a² - y²/b² = 1"},
	{"parabola", "y = a x² + b x + c"},
	{"radians", "π rad = 180°"},

	// ── calculus & differential equations ────────────────────────────────
	{"derivative limit", "f'(x) = lim (f(x+h)-f(x))/h"},
	{"power rule", "d/dx xⁿ = n xⁿ⁻¹"},
	{"product rule", "(f g)' = f' g + f g'"},
	{"quotient rule", "(f/g)' = (f' g - f g')/g²"},
	{"chain rule", "dy/dx = dy/du · du/dx"},
	{"fundamental theorem", "∫ f'(x) dx = f(b) - f(a)"},
	{"integration by parts", "∫ u dv = uv - ∫ v du"},
	{"gaussian integral", "∫ e⁻ˣ² dx = √π"},
	{"exponential integral", "∫ eˣ dx = eˣ + C"},
	{"power integral", "∫ xⁿ dx = xⁿ⁺¹/(n+1)"},
	{"taylor series", "f(x) = Σ f⁽ⁿ⁾(a)/n! (x-a)ⁿ"},
	{"maclaurin exp", "eˣ = Σ xⁿ/n!"},
	{"maclaurin sin", "sin x = Σ (-1)ⁿ x²ⁿ⁺¹/(2n+1)!"},
	{"euler method", "yₙ₊₁ = yₙ + h f(xₙ, yₙ)"},
	{"laplacian", "∇²f = ∂²f/∂x² + ∂²f/∂y²"},
	{"gradient", "∇f = (∂f/∂x, ∂f/∂y, ∂f/∂z)"},
	{"divergence theorem", "∮ F·dA = ∫ ∇·F dV"},
	{"stokes theorem", "∮ F·dr = ∫ (∇×F)·dA"},
	{"greens theorem", "∮ P dx + Q dy = ∫ (∂Q/∂x - ∂P/∂y) dA"},
	{"arc length integral", "L = ∫ √(1 + (dy/dx)²) dx"},
	{"mean value theorem", "f'(c) = (f(b)-f(a))/(b-a)"},
	{"leibniz pi", "π/4 = Σ (-1)ⁿ/(2n+1)"},
	{"wallis product", "π/2 = ∏ (2n)²/((2n-1)(2n+1))"},
	{"heat equation", "∂u/∂t = α ∇²u"},
	{"wave equation", "∂²u/∂t² = c² ∇²u"},
	{"laplace equation", "∇²φ = 0"},
	{"poisson equation", "∇²φ = -ρ/ε₀"},
	{"logistic ode", "dP/dt = r P (1 - P/K)"},
	{"exponential growth", "N(t) = N₀ e^(rt)"},
	{"newton cooling", "dT/dt = -k(T - Tₑ)"},

	// ── linear algebra & probability ─────────────────────────────────────
	{"matrix det 2x2", "det A = ad - bc"},
	{"eigenvalue", "A v = λ v"},
	{"identity matrix", "A I = A"},
	{"inverse", "A A⁻¹ = I"},
	{"trace", "tr A = Σ aᵢᵢ"},
	{"dot product", "u·v = Σ uᵢ vᵢ"},
	{"cross product norm", "‖u×v‖ = ‖u‖ ‖v‖ sin θ"},
	{"cauchy binet", "det(AB) = det A · det B"},
	{"vector norm", "‖v‖ = √(Σ vᵢ²)"},
	{"projection", "proj = (u·v)/‖v‖² v"},
	{"probability union", "P(A ∪ B) = P(A) + P(B) - P(A ∩ B)"},
	{"conditional prob", "P(A|B) = P(A ∩ B)/P(B)"},
	{"bayes theorem", "P(A|B) = P(B|A) P(A)/P(B)"},
	{"expectation", "E[X] = Σ xᵢ pᵢ"},
	{"variance", "Var(X) = E[X²] - E[X]²"},
	{"std deviation", "σ = √(Var(X))"},
	{"binomial dist", "P(k) = C(n,k) pᵏ (1-p)ⁿ⁻ᵏ"},
	{"poisson dist", "P(k) = λᵏ e⁻λ/k!"},
	{"normal pdf", "f(x) = 1/(σ√(2π)) e^(-(x-μ)²/(2σ²))"},
	{"z score", "z = (x - μ)/σ"},
	{"covariance", "Cov(X,Y) = E[XY] - E[X] E[Y]"},
	{"correlation", "ρ = Cov(X,Y)/(σ_x σ_y)"},
	{"law large numbers", "⟨x⟩ → μ"},
	{"markov inequality", "P(X ≥ a) ≤ E[X]/a"},
	{"entropy shannon", "H = -Σ pᵢ log pᵢ"},
	{"geometric mean", "G = (∏ xᵢ)^(1/n)"},
	{"sample mean", "⟨x⟩ = 1/n Σ xᵢ"},
	{"combination", "C(n,k) = n!/(k!(n-k)!)"},
	{"permutation", "P(n,k) = n!/(n-k)!"},
	{"bernoulli", "E[X] = p"},

	// ── classical mechanics & waves ──────────────────────────────────────
	{"newton second", "F = m a"},
	{"momentum", "p = m v"},
	{"impulse", "J = F Δt"},
	{"kinetic energy", "KE = 1/2 m v²"},
	{"potential energy", "PE = m g h"},
	{"work", "W = F · d"},
	{"power", "P = W/t"},
	{"gravitation", "F = G m₁ m₂ / r²"},
	{"kepler third", "T² ∝ a³"},
	{"centripetal", "a = v²/r"},
	{"angular momentum", "L = I ω"},
	{"torque", "τ = r × F"},
	{"hookes law", "F = -k x"},
	{"shm period", "T = 2π √(m/k)"},
	{"pendulum", "T = 2π √(L/g)"},
	{"escape velocity", "vₑ = √(2 G M / r)"},
	{"wave speed", "v = f λ"},
	{"doppler", "f' = f (v ± vₛ)/v"},
	{"snell law", "n₁ sin θ₁ = n₂ sin θ₂"},
	{"lens equation", "1/f = 1/u + 1/v"},
	{"pressure", "P = F/A"},
	{"bernoulli fluid", "P + 1/2 ρ v² + ρ g h = const"},
	{"buoyancy", "F = ρ g V"},
	{"reynolds", "Re = ρ v L / μ"},
	{"rocket", "Δv = vₑ ln(m₀/m)"},
	{"elastic potential", "U = 1/2 k x²"},
	{"free fall", "v = g t"},
	{"projectile range", "R = v² sin(2θ)/g"},

	// ── thermodynamics & statistical mechanics ───────────────────────────
	{"ideal gas", "P V = n R T"},
	{"first law", "ΔU = Q - W"},
	{"entropy", "ΔS = Q/T"},
	{"gibbs free energy", "G = H - T S"},
	{"boltzmann entropy", "S = k ln W"},
	{"stefan boltzmann", "j = σ T⁴"},
	{"wien law", "λₘₐₓ T = b"},
	{"carnot efficiency", "η = 1 - T_c/T_h"},
	{"maxwell boltzmann", "f(v) ∝ v² e^(-m v²/(2 k T))"},
	{"heat capacity", "Q = m c ΔT"},
	{"thermal conduction", "q = -k ∇T"},
	{"boltzmann factor", "P ∝ e^(-E/(k T))"},
	{"partition function", "Z = Σ e^(-Eᵢ/(k T))"},
	{"clausius", "∮ δQ/T ≤ 0"},
	{"van der waals", "(P + a/V²)(V - b) = R T"},

	// ── electromagnetism ─────────────────────────────────────────────────
	{"coulomb law", "F = k q₁ q₂ / r²"},
	{"ohm law", "V = I R"},
	{"electric power", "P = V I"},
	{"capacitance", "C = Q/V"},
	{"gauss law", "∮ E·dA = Q/ε₀"},
	{"gauss magnetism", "∮ B·dA = 0"},
	{"faraday law", "∮ E·dl = -dΦ/dt"},
	{"ampere law", "∮ B·dl = μ₀ I"},
	{"lorentz force", "F = q(E + v × B)"},
	{"electric field", "E = F/q"},
	{"biot savart", "dB = μ₀/(4π) I dl × r / r³"},
	{"poynting", "S = 1/μ₀ E × B"},
	{"energy density", "u = 1/2 ε₀ E²"},
	{"rc time constant", "τ = R C"},
	{"inductor emf", "ε = -L dI/dt"},
	{"speed of light", "c = 1/√(μ₀ ε₀)"},

	// ── quantum, relativity, modern physics ──────────────────────────────
	{"mass energy", "E = m c²"},
	{"planck relation", "E = h ν"},
	{"de broglie", "λ = h/p"},
	{"uncertainty", "Δx Δp ≥ ħ/2"},
	{"schrodinger", "iħ ∂ψ/∂t = H ψ"},
	{"schrodinger stationary", "H ψ = E ψ"},
	{"energy time uncertainty", "ΔE Δt ≥ ħ/2"},
	{"photon momentum", "p = h/λ"},
	{"compton", "Δλ = h/(mₑ c)(1 - cos θ)"},
	{"bohr radius", "r = 4π ε₀ ħ²/(mₑ e²)"},
	{"rydberg", "1/λ = R(1/n₁² - 1/n₂²)"},
	{"time dilation", "Δt = γ Δt₀"},
	{"length contraction", "L = L₀/γ"},
	{"lorentz factor", "γ = 1/√(1 - v²/c²)"},
	{"relativistic energy", "E² = (p c)² + (m c²)²"},
	{"momentum relativistic", "p = γ m v"},
	{"planck energy", "E = ħ ω"},
	{"fine structure", "α = e²/(4π ε₀ ħ c)"},
	{"blackbody planck", "B ∝ 1/(e^(hν/(kT)) - 1)"},
	{"casimir", "F ∝ -ħ c/d⁴"},

	// ── chemistry, biology, cs, finance ──────────────────────────────────
	{"combustion methane", "CH₄ + 2 O₂ → CO₂ + 2 H₂O"},
	{"water autoionization", "H₂O ⇌ H⁺ + OH⁻"},
	{"ph", "pH = -log[H⁺]"},
	{"nernst", "E = E° - (R T)/(n F) ln Q"},
	{"arrhenius", "k = A e^(-Eₐ/(R T))"},
	{"gibbs equilibrium", "ΔG = -R T ln K"},
	{"henderson", "pH = pKₐ + log([A]/[HA])"},
	{"beer lambert", "A = ε l c"},
	{"hardy weinberg", "p² + 2pq + q² = 1"},
	{"michaelis menten", "v = Vₘₐₓ [S]/(Kₘ + [S])"},
	{"shannon capacity", "C = B log(1 + S/N)"},
	{"master theorem", "T(n) = a T(n/b) + f(n)"},
	{"big o", "f(n) = O(n log n)"},
	{"euclid gcd", "gcd(a,b) = gcd(b, a mod b)"},
	{"compound interest", "A = P(1 + r/n)^(n t)"},
	{"continuous interest", "A = P e^(r t)"},
	{"present value", "PV = FV/(1 + r)ⁿ"},
	{"black scholes", "C = S N(d₁) - K e^(-r t) N(d₂)"},
	{"drake equation", "N = R⋆ fₚ nₑ f_l fᵢ f_c L"},
	{"logistic map", "xₙ₊₁ = r xₙ(1 - xₙ)"},

	// ── logic, bitwise, tensors, ML ──────────────────────────────────────
	{"de morgan and", "not(a and b) = (not a) or (not b)"},
	{"de morgan or", "not(a or b) = (not a) and (not b)"},
	{"xor definition", "a xor b = (a or b) and not(a and b)"},
	{"implication", "a => b = (not a) or b"},
	{"absorption", "a or (a and b) = a"},
	{"contrapositive", "(a => b) <=> (not b => not a)"},
	{"boolean identity", "a and 1 = a"},
	{"left shift", "a << n = a · 2ⁿ"},
	{"right shift", "a >> n = ⌊a/2ⁿ⌋"},
	{"bitwise xor", "a ⊕ b = (a ∨ b) ∧ ¬(a ∧ b)"},
	{"modulo", "a mod n = a - n ⌊a/n⌋"},
	{"comparison chain", "0 <= i && i < n"},
	{"not equal", "a != b"},
	{"assignment", "x := x + 1"},
	{"quantifier", "∀x ∃y (x < y)"},
	{"einstein summation", "cᵢₖ = aᵢⱼ bⱼₖ"},
	{"einsum matmul", "einsum(ij,jk->ik, A, B)"},
	{"tensor product", "A ⊗ B"},
	{"hadamard product", "(A ⊙ B)ᵢⱼ = Aᵢⱼ Bᵢⱼ"},
	{"kronecker delta", "δᵢⱼ = 1 if i = j"},
	{"dot as sum", "a·b = Σ aᵢ bᵢ"},
	{"sigmoid", "σ(x) = 1/(1 + e⁻ˣ)"},
	{"softmax", "softmax(z)ᵢ = e^(zᵢ)/Σ e^(zⱼ)"},
	{"relu", "f(x) = max(0, x)"},
	{"cross entropy", "H = -Σ pᵢ log qᵢ"},
	{"gradient descent", "θ := θ - α ∇J"},
	{"floor bound", "⌊x⌋ <= x < ⌊x⌋ + 1"},
}

// TestMathCorpus is the coverage net: every one of the 200 famous equations must
// convert to LaTeX with NO unmapped unicode symbol left behind (each such leak is
// an operator/symbol still missing from mathSym), and the coloring vocabulary
// must tint every operator rune in the source.
func TestMathCorpus(t *testing.T) {
	if len(corpus) < 200 {
		t.Fatalf("corpus has %d equations, want at least 200", len(corpus))
	}
	seen := map[string]bool{}
	for _, eq := range corpus {
		if seen[eq.name] {
			t.Errorf("duplicate corpus name %q", eq.name)
		}
		seen[eq.name] = true
		t.Run(eq.name, func(t *testing.T) {
			it := mleaf(eq.expr)
			lx := mathToLatex(it)
			if lx == "" {
				t.Fatalf("empty LaTeX for %q", eq.expr)
			}
			for _, r := range lx {
				if r > 127 {
					t.Errorf("unmapped symbol %q (U+%04X) survived into LaTeX %q — add it to mathSym",
						string(r), r, lx)
				}
			}
			cols := mathSpanColor(it, []rune(eq.expr))
			for i, r := range []rune(eq.expr) {
				if mathIsOpRune(r) && cols[i] != cYellow {
					t.Errorf("operator %q (index %d) in %q was not tinted", string(r), i, eq.expr)
				}
			}
		})
	}
}
