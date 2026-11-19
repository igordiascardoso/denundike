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
  register: function (login, email, password, inviteCode) {
    return window.bridge.api('POST', '/api/auth/register', {
      login: login,
      email: email,
      password: password,
      invite_code: inviteCode,
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
