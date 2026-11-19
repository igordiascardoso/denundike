// api.js — wrappers over window.bridge.api
// Expõe window.api com métodos de alto nível para auth e convites.
// Base URL configurável via DENUNDIKE_SERVER (env var lida pelo preload).

/* global bridge */

var api = {
  /**
   * Login com e-mail/login + senha.
   * @returns {Promise<{token: string, user: object}>}
   */
  login: function (login, password) {
    return window.bridge.api('POST', '/api/auth/login', {
      login: login,
      password: password,
    });
  },

  /**
   * Registro de nova conta.
   * @returns {Promise<{user: object}>}
   */
  register: function (login, email, password, inviteCode, displayName) {
    return window.bridge.api('POST', '/api/auth/register', {
      login: login,
      email: email,
      display_name: displayName || '',
      password: password,
      invite_code: inviteCode,
    });
  },

  /** Sessão salva: valida o token guardado. Rejeita com 401 se expirou. */
  me: function (token) {
    return window.bridge.api('GET', '/api/auth/me', null, token);
  },

  /** Redefine a senha com o código de recuperação; devolve {token,user,recovery_code}. */
  recover: function (login, recoveryCode, newPassword) {
    return window.bridge.api('POST', '/api/auth/recover', {
      login: login,
      recovery_code: recoveryCode,
      new_password: newPassword,
    });
  },

  /**
   * Busca informações de um convite (endpoint público).
   * @returns {Promise<{created_by_login: string, valid: boolean, expires_at: string}>}
   */
  getInvite: function (code) {
    return window.bridge.api('GET', '/api/invites/' + encodeURIComponent(code));
  },
};
